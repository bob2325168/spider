package auth

import (
	"context"
	"errors"
	"go-micro.dev/v4"
	"go-micro.dev/v4/auth"
	"go-micro.dev/v4/metadata"
	"go-micro.dev/v4/server"
	"strings"
)

func AuthWrapper(service micro.Service) server.HandlerWrapper {
	return func(fn server.HandlerFunc) server.HandlerFunc {
		return func(ctx context.Context, req server.Request, rsp interface{}) error {

			md, b := metadata.FromContext(ctx)
			if !b {
				return errors.New("no metadata found in request")
			}

			// 获取auth header
			header, ok := md["Authorization"]
			if !ok || strings.HasPrefix(header, auth.BearerScheme) {
				return errors.New("no authorization token found")
			}

			// 解析token
			token := strings.TrimPrefix(header, auth.BearerScheme)

			// 验证token
			a := service.Options().Auth
			_, err := a.Inspect(token)
			if err != nil {
				return errors.New("invalid token")
			}

			return fn(ctx, req, rsp)
		}
	}
}
