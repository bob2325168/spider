package main

import (
	"context"
	"fmt"
	"github.com/bob2325168/spider/collect"
	"github.com/bob2325168/spider/engine"
	"github.com/bob2325168/spider/limiter"
	"github.com/bob2325168/spider/logger"
	gt "github.com/bob2325168/spider/proto/greeter"
	"github.com/bob2325168/spider/proxy"
	"github.com/bob2325168/spider/storage"
	"github.com/bob2325168/spider/storage/sqlstorage"
	etcdReg "github.com/go-micro/plugins/v4/registry/etcd"
	gs "github.com/go-micro/plugins/v4/server/grpc"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go-micro.dev/v4"
	"go-micro.dev/v4/registry"
	"go-micro.dev/v4/server"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"net/http"
	"time"
)

func main() {

	// 设置标准的日志输入格式
	plugin := logger.NewStdoutPlugin(zapcore.DebugLevel)
	newLogger := logger.NewLogger(plugin)
	newLogger.Info("log init end")

	// 设置zap全局的logger
	zap.ReplaceGlobals(newLogger)

	proxyURLs := []string{"http://127.0.0.1:7890", "http://127.0.0.1:7890"}
	var p proxy.Func
	var err error
	if p, err = proxy.RoundRobinProxySwitcher(proxyURLs...); err != nil {
		newLogger.Error("roundRobinProxySwitcher failed")
		return
	}

	//设置存储
	var store storage.Storage
	if store, err = sqlstorage.NewSqlStore(
		sqlstorage.WithSql("root:123456@tcp(127.0.0.1:3306)/crawler?charset=utf8"),
		sqlstorage.WithLogger(newLogger.Named("sqlDB")),
		sqlstorage.WithBatchCount(2),
	); err != nil {
		newLogger.Error("create sqldb failed")
		return
	}

	var f collect.Fetcher = &collect.BrowserFetch{
		Timeout: 5 * time.Second,
		Logger:  newLogger,
		Proxy:   p,
	}

	// 设置限速器
	// 2秒钟1个
	secondLimit := rate.NewLimiter(limiter.Per(1, 2*time.Second), 1)
	// 60秒20个
	minuteLimit := rate.NewLimiter(limiter.Per(20, 1*time.Minute), 20)
	multiLimiter := limiter.Multi(secondLimit, minuteLimit)

	// 初始化task
	var seeds = make([]*collect.Task, 0, 1000)
	// 构造seeds
	seeds = append(seeds, &collect.Task{
		Property: collect.Property{
			Name: "douban_book_list",
		},
		Fetcher: f,
		Storage: store,
		Limiter: multiLimiter,
	})

	s := engine.NewEngine(
		engine.WithFetcher(f),
		engine.WithLogger(newLogger),
		engine.WithWorkCount(5),
		engine.WithSeeds(seeds),
		engine.WithScheduler(engine.NewSchedule()),
	)

	// 启动worker
	go s.Run()

	// 启动http proxy to grpc
	go HandleHTTP()

	// 启动grpc服务器
	reg := etcdReg.NewRegistry(
		registry.Addrs(":2379"),
	)

	service := micro.NewService(
		micro.Server(
			gs.NewServer(
				server.Id("1"),
			)),
		micro.Address(":9090"),
		micro.Registry(reg),
		micro.Name("go.micro.sever.worker"),
		micro.WrapHandler(logWrapper(newLogger)),
	)
	service.Init()
	gt.RegisterGreeterHandler(service.Server(), new(Greeter))
	//启动GRPC服务
	if err := service.Run(); err != nil {
		newLogger.Fatal("grpc server stop")
	}
}

func HandleHTTP() {

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	mux := runtime.NewServeMux()
	opts := []grpc.DialOption{grpc.WithInsecure()}

	if err := gt.RegisterGreeterGwFromEndpoint(ctx, mux, "localhost:9090", opts); err != nil {
		fmt.Println(err)
	}
	// 启动http服务
	http.ListenAndServe(":8080", mux)
}

// 使用go函数闭包的特性，对请求进行封装
// 中间件函数在接收到GRPC请求时，可以打印出请求的具体参数，方便排查问题
func logWrapper(log *zap.Logger) server.HandlerWrapper {
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

type Greeter struct {
}

func (g Greeter) Hello(ctx context.Context, request *gt.Request, response *gt.Response) error {
	response.Greeting = "Hello" + request.Name
	return nil
}
