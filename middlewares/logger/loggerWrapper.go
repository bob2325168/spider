package logger

import (
	"context"
	"go-micro.dev/v4/server"
	"go.uber.org/zap"
)

// LogWrapper 使用go函数闭包的特性，对请求进行封装
// 中间件函数在接收到GRPC请求时，可以打印出请求的具体参数，方便排查问题
func LogWrapper(log *zap.Logger) server.HandlerWrapper {
	return func(fn server.HandlerFunc) server.HandlerFunc {
		return func(ctx context.Context, req server.Request, rsp interface{}) error {
			log.Info("receive request",
				zap.String("method", req.Method()),
				zap.String("service", req.Service()),
				zap.Reflect("request param:", req.Body()),
			)
			err := fn(ctx, req, rsp)
			return err
		}
	}
}
