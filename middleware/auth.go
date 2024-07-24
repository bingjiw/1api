package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common/blacklist"
	"github.com/songquanpeng/one-api/common/ctxkey"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/common/network"
	"github.com/songquanpeng/one-api/model"
)

func authHelper(c *gin.Context, minRole int) {
	session := sessions.Default(c)
	username := session.Get("username")
	role := session.Get("role")
	id := session.Get("id")
	status := session.Get("status")
	if username == nil {
		// Check access token
		accessToken := c.Request.Header.Get("Authorization")
		if accessToken == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "无权进行此操作，未登录且未提供 access token",
			})
			c.Abort()
			return
		}
		user := model.ValidateAccessToken(accessToken)
		if user != nil && user.Username != "" {
			// Token is valid
			username = user.Username
			role = user.Role
			id = user.Id
			status = user.Status
		} else {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无权进行此操作，access token 无效",
			})
			c.Abort()
			return
		}
	}
	if status.(int) == model.UserStatusDisabled || blacklist.IsUserBanned(id.(int)) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "用户已被封禁",
		})
		session := sessions.Default(c)
		session.Clear()
		_ = session.Save()
		c.Abort()
		return
	}
	if role.(int) < minRole {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无权进行此操作，权限不足",
		})
		c.Abort()
		return
	}
	c.Set("username", username)
	c.Set("role", role)
	c.Set("id", id)
	c.Next()
}

func UserAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authHelper(c, model.RoleCommonUser)
	}
}

func AdminAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authHelper(c, model.RoleAdminUser)
	}
}

func RootAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authHelper(c, model.RoleRootUser)
	}
}

func TokenAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		key := c.Request.Header.Get("Authorization")
		key = strings.TrimPrefix(key, "Bearer ")
		key = strings.TrimPrefix(key, "sk-")
		parts := strings.Split(key, "-")
		key = parts[0]
		token, err := model.ValidateUserToken(key)
		if err != nil {

			//VVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVV
			//炳：如果请求所附带的token不存在，就在数据库中插入一个（以传入的key值）新建的token
			if err.Error() == "无效的令牌" {

				// create default token
				cleanToken := model.Token{
					UserId:         1,             //1	就是 root 超级管理员
					Name:           "自动创令牌" + key, //"default",
					Key:            key,           //random.GenerateKey(),
					CreatedTime:    0,             //借用此来存储令牌被请求的次数，来记录问答次数。helper.GetTimestamp(),
					AccessedTime:   helper.GetTimestamp(),
					ExpiredTime:    -1,
					RemainQuota:    -1,
					UnlimitedQuota: true,
				}
				insertTokenError := cleanToken.Insert()
				if insertTokenError == nil {
					// 往数据库新增 刚创建的token 成功
					token = &cleanToken
					logger.SysLog(fmt.Sprintf("给不存在的token 往数据库新增 刚创建的token 成功: %s", cleanToken.Name))
				} else {
					// 往数据库新增 刚创建的token 失败
					logger.SysError(fmt.Sprintf("给不存在的token 往数据库新增 刚创建的token 失败: %s", insertTokenError.Error()))
				}
			} else {
				//以下2句是原代码
				abortWithMessage(c, http.StatusUnauthorized, err.Error())
				return
			}
			//AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA

		}
		if token.Subnet != nil && *token.Subnet != "" {
			if !network.IsIpInSubnets(ctx, c.ClientIP(), *token.Subnet) {
				abortWithMessage(c, http.StatusForbidden, fmt.Sprintf("该令牌只能在指定网段使用：%s，当前 ip：%s", *token.Subnet, c.ClientIP()))
				return
			}
		}
		userEnabled, err := model.CacheIsUserEnabled(token.UserId)
		if err != nil {
			abortWithMessage(c, http.StatusInternalServerError, err.Error())
			return
		}
		if !userEnabled || blacklist.IsUserBanned(token.UserId) {
			abortWithMessage(c, http.StatusForbidden, "用户已被封禁")
			return
		}
		requestModel, err := getRequestModel(c)
		if err != nil && shouldCheckModel(c) {
			abortWithMessage(c, http.StatusBadRequest, err.Error())
			return
		}
		c.Set(ctxkey.RequestModel, requestModel)
		if token.Models != nil && *token.Models != "" {
			c.Set(ctxkey.AvailableModels, *token.Models)
			if requestModel != "" && !isModelInList(requestModel, *token.Models) {
				abortWithMessage(c, http.StatusForbidden, fmt.Sprintf("该令牌无权使用模型：%s", requestModel))
				return
			}
		}
		c.Set(ctxkey.Id, token.UserId)
		c.Set(ctxkey.TokenId, token.Id)
		c.Set(ctxkey.TokenName, token.Name)
		if len(parts) > 1 {
			if model.IsAdmin(token.UserId) {
				c.Set(ctxkey.SpecificChannelId, parts[1])
			} else {
				abortWithMessage(c, http.StatusForbidden, "普通用户不支持指定渠道")
				return
			}
		}

		//炳 VVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVV
		//借用created_time来记某令牌的请求（问答）的次数 +1
		token.CreatedTime = token.CreatedTime + 1
		token.Update() //保存到数据库
		//AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA

		c.Next()
	}
}

func shouldCheckModel(c *gin.Context) bool {
	if strings.HasPrefix(c.Request.URL.Path, "/v1/completions") {
		return true
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/chat/completions") {
		return true
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/images") {
		return true
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/audio") {
		return true
	}
	return false
}
