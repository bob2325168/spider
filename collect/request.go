package collect

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"github.com/bob2325168/spider/limiter"
	"github.com/bob2325168/spider/storage"
	"go.uber.org/zap"
	"golang.org/x/net/context"
	"math/rand"
	"regexp"
	"sync"
	"time"
)

type ParseResult struct {
	Requests []*Request
	Items    []interface{}
}

type Property struct {
	Name     string `json:"name"` // 任务名称，是全局唯一的
	Url      string `json:"url"`
	Cookie   string `json:"cookie"`
	WaitTime int64  `json:"wait_time"` // 随机休眠时间，单位是秒
	MaxDepth int64  `json:"max_depth"` // 爬取的最大深度
	Reload   bool   `json:"reload"`    // 网站是否可以重复爬取
}

// Task 一个任务实例
type Task struct {
	Property
	Visited     map[string]bool //是否爬过该网站
	VisitedLock sync.Mutex
	Fetcher     Fetcher
	Rule        RuleTree
	Storage     storage.Storage
	Logger      *zap.Logger
	Limiter     limiter.RateLimiter
}

// Request 单个任务请求
type Request struct {
	unique   string
	Method   string
	Task     *Task
	URL      string
	Depth    int64
	Priority int64
	RuleName string
	TmpData  *Temp
}

type Context struct {
	Req  *Request
	Body []byte
}

func (r *Request) Check() error {
	if r.Depth > r.Task.MaxDepth {
		return errors.New("max depth limit exceeded")
	}
	return nil
}

// Unique 生成请求的唯一识别码
func (r *Request) Unique() string {
	block := md5.Sum([]byte(r.URL + r.Method))
	return hex.EncodeToString(block[:])
}

// ParseJsReg parse规则
func (ctx *Context) ParseJsReg(name string, reg string) ParseResult {
	re := regexp.MustCompile(reg)

	matches := re.FindAllSubmatch(ctx.Body, -1)
	result := ParseResult{}

	for _, m := range matches {
		u := string(m[1])
		result.Requests = append(
			result.Requests, &Request{
				Method:   "GET",
				Task:     ctx.Req.Task,
				URL:      u,
				Depth:    ctx.Req.Depth + 1,
				RuleName: name,
			})
	}
	return result
}

func (ctx *Context) OutputJs(reg string) ParseResult {
	re := regexp.MustCompile(reg)
	ok := re.Match(ctx.Body)
	if !ok {
		return ParseResult{
			Items: []interface{}{},
		}
	}
	result := ParseResult{
		Items: []interface{}{ctx.Req.URL},
	}
	return result
}

func (ctx *Context) Output(data interface{}) *storage.DataCell {
	res := &storage.DataCell{}
	res.Data = make(map[string]interface{})
	res.Data["Task"] = ctx.Req.Task.Name
	res.Data["Rule"] = ctx.Req.RuleName
	res.Data["Data"] = data
	res.Data["URL"] = ctx.Req.URL
	res.Data["Time"] = time.Now().Format("2006-01-02 15:04:05")
	return res
}

func (ctx *Context) GetRule(ruleName string) *Rule {
	return ctx.Req.Task.Rule.Trunk[ruleName]
}

func (r *Request) Fetch() ([]byte, error) {

	if err := r.Task.Limiter.Wait(context.Background()); err != nil {
		return nil, err
	}
	sleepTime := rand.Int63n(r.Task.WaitTime)
	time.Sleep(time.Duration(sleepTime))
	return r.Task.Fetcher.Get(r)
}
