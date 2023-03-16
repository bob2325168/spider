package spider

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"math/rand"
	"time"
)

// Request 单个任务请求
type Request struct {
	Method   string
	Task     *Task
	URL      string
	Depth    int64
	Priority int64
	RuleName string
	TmpData  *Temp
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

func (r *Request) Fetch() ([]byte, error) {

	if err := r.Task.Limiter.Wait(context.Background()); err != nil {
		return nil, err
	}

	sleepTime := rand.Int63n(r.Task.WaitTime * 1000)
	time.Sleep(time.Duration(sleepTime) * time.Millisecond)

	return r.Task.Fetcher.Get(r)
}
