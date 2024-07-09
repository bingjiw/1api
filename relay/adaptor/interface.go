package adaptor

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/relay/meta"
	"github.com/songquanpeng/one-api/relay/model"
)

type Adaptor interface {
	Init(meta *meta.Meta)
	GetRequestURL(meta *meta.Meta) (string, error)
	SetupRequestHeader(c *gin.Context, req *http.Request, meta *meta.Meta) error
	ConvertRequest(c *gin.Context, relayMode int, request *model.GeneralOpenAIRequest) (any, error)
	ConvertImageRequest(request *model.ImageRequest) (any, error)
	DoRequest(c *gin.Context, meta *meta.Meta, requestBody io.Reader) (*http.Response, error)

	// 炳：修改以上GO代码中的DoResponse的定义，使其增加一个返回参数 string，但如果implementation没有返回这个参数也不出错，如果及调用者没有取这个参数也不出错。
	// 这里的修改：
	// 在 DoResponse 方法签名中添加了 content string 作为第三个返回值。
	// 由于 Go 语言的特性，这种修改是向后兼容的：
	// 如果某个实现没有返回这个新的 string 参数，Go 编译器会自动用该类型的零值（空字符串）填充。
	// 如果调用者不使用这个新的返回值，Go 允许忽略函数调用的某些返回值，所以也不会出错。
	// 这种修改允许您逐步更新代码，而不会立即破坏现有的实现或调用。您可以在需要的地方逐步添加对新返回值的支持。
	// 原本想如下这样干的，但是：在Go语言中，如果一个结构体要实现一个接口，它必须实现接口中的所有方法。否则，编译器会报错，表示该结构体没有完全实现接口。
	// 所以就算了吧。
	//DoResponse(c *gin.Context, resp *http.Response, meta *meta.Meta) (usage *model.Usage, err *model.ErrorWithStatusCode, strResponseText string)

	DoResponse(c *gin.Context, resp *http.Response, meta *meta.Meta) (usage *model.Usage, err *model.ErrorWithStatusCode)

	GetModelList() []string
	GetChannelName() string
}
