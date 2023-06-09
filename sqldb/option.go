package sqldb

import (
	"go.uber.org/zap"
)

type options struct {
	logger *zap.Logger
	sqlURL string
}

var defaultOptions = options{
	logger: zap.NewNop(),
}

// Option 选项模式，可以设置默认值
type Option func(opts *options)

func WithLogger(logger *zap.Logger) Option {
	return func(opts *options) {
		opts.logger = logger
	}
}

func WithConnUrl(sqlURL string) Option {
	return func(opts *options) {
		opts.sqlURL = sqlURL
	}
}
