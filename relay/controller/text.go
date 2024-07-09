package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/relay"
	"github.com/songquanpeng/one-api/relay/adaptor/openai"
	"github.com/songquanpeng/one-api/relay/apitype"
	"github.com/songquanpeng/one-api/relay/billing"
	billingratio "github.com/songquanpeng/one-api/relay/billing/ratio"
	"github.com/songquanpeng/one-api/relay/channeltype"
	"github.com/songquanpeng/one-api/relay/meta"
	"github.com/songquanpeng/one-api/relay/model"
)

func RelayTextHelper(c *gin.Context) *model.ErrorWithStatusCode {
	ctx := c.Request.Context()
	meta := meta.GetByContext(c)
	// get & validate textRequest
	textRequest, err := getAndValidateTextRequest(c, meta.Mode)
	if err != nil {
		logger.Errorf(ctx, "getAndValidateTextRequest failed: %s", err.Error())
		return openai.ErrorWrapper(err, "invalid_text_request", http.StatusBadRequest)
	}
	meta.IsStream = textRequest.Stream

	// map model name
	var isModelMapped bool
	meta.OriginModelName = textRequest.Model
	textRequest.Model, isModelMapped = getMappedModelName(textRequest.Model, meta.ModelMapping)
	meta.ActualModelName = textRequest.Model
	// get model ratio & group ratio
	modelRatio := billingratio.GetModelRatio(textRequest.Model)
	groupRatio := billingratio.GetGroupRatio(meta.Group)
	ratio := modelRatio * groupRatio
	// pre-consume quota
	promptTokens := getPromptTokens(textRequest, meta.Mode)
	meta.PromptTokens = promptTokens
	preConsumedQuota, bizErr := preConsumeQuota(ctx, textRequest, promptTokens, ratio, meta)
	if bizErr != nil {
		logger.Warnf(ctx, "preConsumeQuota failed: %+v", *bizErr)
		return bizErr
	}

	adaptor := relay.GetAdaptor(meta.APIType)
	if adaptor == nil {
		return openai.ErrorWrapper(fmt.Errorf("invalid api type: %d", meta.APIType), "invalid_api_type", http.StatusBadRequest)
	}
	adaptor.Init(meta)

	// get request body
	var requestBody io.Reader
	if meta.APIType == apitype.OpenAI {
		// no need to convert request for openai
		shouldResetRequestBody := isModelMapped || meta.ChannelType == channeltype.Baichuan // frequency_penalty 0 is not acceptable for baichuan
		if shouldResetRequestBody {
			jsonStr, err := json.Marshal(textRequest)
			if err != nil {
				return openai.ErrorWrapper(err, "json_marshal_failed", http.StatusInternalServerError)
			}
			requestBody = bytes.NewBuffer(jsonStr)
		} else {
			requestBody = c.Request.Body
		}
	} else {
		convertedRequest, err := adaptor.ConvertRequest(c, meta.Mode, textRequest)
		if err != nil {
			return openai.ErrorWrapper(err, "convert_request_failed", http.StatusInternalServerError)
		}
		jsonData, err := json.Marshal(convertedRequest)
		if err != nil {
			return openai.ErrorWrapper(err, "json_marshal_failed", http.StatusInternalServerError)
		}
		logger.Debugf(ctx, "converted request: \n%s", string(jsonData))
		requestBody = bytes.NewBuffer(jsonData)
	}

	// do request
	resp, err := adaptor.DoRequest(c, meta, requestBody)

	//炳：在上一句的resp中只拿到初始的回复，不是完整的所有流式内容
	//所以不能在这里开刀处理

	if err != nil {
		logger.Errorf(ctx, "DoRequest failed: %s", err.Error())
		return openai.ErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}
	if isErrorHappened(meta, resp) {
		billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
		return RelayErrorHandler(resp)
	}

	/* 	炳：关于流式的回复内容，何时拿到？

	   	等待时间：
	   	是的，代码会在 DoResponse 这里等待，直到所有的流式内容都被接收和处理完毕。这可能需要10秒或更长时间，取决于服务器响应的完整时间。
	   	"卡住"的性质：
	   	虽然 DoResponse 确实在等待所有内容，但它并不是完全"卡住"或阻塞的。
	   	在等待期间，它会持续处理接收到的数据流，并可能将处理后的数据实时发送给客户端。

	   	异步性：
	   	尽管 DoResponse 在等待，但它通常不会阻塞整个服务器或其他请求的处理。
	   	在许多实现中，这个过程可能运行在一个 goroutine 中，允许并发处理其他请求。

	   	返回时机：
	   	只有当所有流式内容都被接收和处理完毕后，DoResponse 才会返回。 */

	// post-consume quota
	//炳，原句：go postConsumeQuota(ctx, usage, meta, textRequest, ratio, preConsumedQuota, modelRatio, groupRatio)
	//改为如下：
	//responseBytes, anError := io.ReadAll(resp.Body)
	//strResponseText = string(responseBytes)
	//go BJ_postConsumeQuota_withResponseText(strResponseText, ctx, usage, meta, textRequest, ratio, preConsumedQuota, modelRatio, groupRatio)
	//为了使以上代码更健壮并减少潜在的错误，我们可以添加更多的错误处理和资源管理措施。下面是改进后的代码：
	//
	// 确保在函数结束时关闭响应主体
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			// 记录关闭错误，但不覆盖主要错误
			logger.Errorf(ctx, "failed to close response body: %v", cerr)
		}
	}()

	// 读取回复的主体内容
	responseBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		// 处理读取错误
		logger.Errorf(ctx, "failed to read response body: %v", readErr)
		return openai.ErrorWrapper(readErr, "failed_to_read_response_body", http.StatusInternalServerError)
	}

	// 将主体内容转换为字符串
	strResponseText := string(responseBytes)

	// 重新将主体内容放回 resp.Body 中，以便不影响其他代码可能的后续操作
	resp.Body = io.NopCloser(bytes.NewBuffer(responseBytes))

	//原有代码 do response
	usage, respErr := adaptor.DoResponse(c, resp, meta)
	//原有代码
	if respErr != nil {
		logger.Errorf(ctx, "respErr is not nil: %+v", respErr)
		billing.ReturnPreConsumedQuota(ctx, preConsumedQuota, meta.TokenId)
		return respErr
	}

	// 异步调用 postConsumeQuota
	go BJ_postConsumeQuota_withResponseText(strResponseText, c.Request.Context(), usage, meta, textRequest, ratio, preConsumedQuota, modelRatio, groupRatio)

	return nil
}
