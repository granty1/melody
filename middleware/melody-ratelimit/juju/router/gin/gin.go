package gin

import (
	"net/http"
	"strings"

	"melody/config"
	"melody/proxy"

	melodygin "melody/router/gin"

	melodyrate "melody/middleware/melody-ratelimit"

	"github.com/gin-gonic/gin"

	"melody/middleware/melody-ratelimit/juju"
	"melody/middleware/melody-ratelimit/juju/router"
)

// HandlerFactory 是一个立即可使用的基本ratelimit处理程序工厂，它使用默认的melody endpoint handler来处理gin路由器
var HandlerFactory = NewRateLimiterMw(melodygin.EndpointHandler)

// NewRateLimiterMw 在接收的HandlerFactory上构建一个速率限制包装。
func NewRateLimiterMw(next melodygin.HandlerFactory) melodygin.HandlerFactory {
	return func(remote *config.EndpointConfig, p proxy.Proxy) gin.HandlerFunc {
		handlerFunc := next(remote, p)

		cfg := router.ConfigGetter(remote.ExtraConfig).(router.Config)
		if cfg == router.ZeroCfg || (cfg.MaxRate <= 0 && cfg.ClientMaxRate <= 0) {
			return handlerFunc
		}

		if cfg.MaxRate > 0 {
			handlerFunc = NewEndpointRateLimiterMw(juju.NewLimiter(float64(cfg.MaxRate), cfg.MaxRate))(handlerFunc)
		}
		if cfg.ClientMaxRate > 0 {
			switch strings.ToLower(cfg.Strategy) {
			case "ip":
				handlerFunc = NewIpLimiterMw(float64(cfg.ClientMaxRate), cfg.ClientMaxRate)(handlerFunc)
			case "header":
				handlerFunc = NewHeaderLimiterMw(cfg.Key, float64(cfg.ClientMaxRate), cfg.ClientMaxRate)(handlerFunc)
			}
		}
		return handlerFunc
	}
}

// EndpointMw 是一个函数，它用一些速率限制逻辑装饰接收的handlerFunc
type EndpointMw func(gin.HandlerFunc) gin.HandlerFunc

// NewEndpointRateLimiterMw 为给定的 handlerFunc 创建一个简单的速率限制器
func NewEndpointRateLimiterMw(tb juju.Limiter) EndpointMw {
	return func(next gin.HandlerFunc) gin.HandlerFunc {
		return func(c *gin.Context) {
			if !tb.Allow() {
				c.AbortWithError(503, melodyrate.ErrLimited)
				return
			}
			next(c)
		}
	}
}

// NewHeaderLimiterMw creates a token ratelimiter using the value of a header as a token
func NewHeaderLimiterMw(header string, maxRate float64, capacity int64) EndpointMw {
	return NewTokenLimiterMw(HeaderTokenExtractor(header), juju.NewMemoryStore(maxRate, capacity))
}

// NewIpLimiterMw creates a token ratelimiter
func NewIpLimiterMw(maxRate float64, capacity int64) EndpointMw {
	return NewTokenLimiterMw(IPTokenExtractor, juju.NewMemoryStore(maxRate, capacity))
}

// TokenExtractor defines the interface of the functions to use in order to extract a token for each request
type TokenExtractor func(*gin.Context) string

// IPTokenExtractor extracts the IP of the request
func IPTokenExtractor(c *gin.Context) string { return strings.Split(c.ClientIP(), ":")[0] }

// HeaderTokenExtractor returns a TokenExtractor that looks for the value of the designed header
func HeaderTokenExtractor(header string) TokenExtractor {
	return func(c *gin.Context) string { return c.Request.Header.Get(header) }
}

// NewTokenLimiterMw returns a token based ratelimiting endpoint middleware with the received TokenExtractor and LimiterStore
func NewTokenLimiterMw(tokenExtractor TokenExtractor, limiterStore melodyrate.LimiterStore) EndpointMw {
	return func(next gin.HandlerFunc) gin.HandlerFunc {
		return func(c *gin.Context) {
			tokenKey := tokenExtractor(c)
			if tokenKey == "" {
				c.AbortWithError(http.StatusTooManyRequests, melodyrate.ErrLimited)
				return
			}
			if !limiterStore(tokenKey).Allow() {
				c.AbortWithError(http.StatusTooManyRequests, melodyrate.ErrLimited)
				return
			}
			next(c)
		}
	}
}
