package collect

import (
	"bufio"
	"fmt"
	"github.com/bob2325168/spider/extension"
	"github.com/bob2325168/spider/proxy"
	"go.uber.org/zap"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
	"io"
	"net/http"
	"time"
)

type Fetcher interface {
	Get(url *Request) ([]byte, error)
}

type BaseFetch struct {
}

func (BaseFetch) Get(req *Request) ([]byte, error) {
	resp, err := http.Get(req.URL)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, err
	}
	bodyReader := bufio.NewReader(resp.Body)
	e := DetermineEncoding(bodyReader)
	utf8Reader := transform.NewReader(bodyReader, e.NewDecoder())
	return io.ReadAll(utf8Reader)
}

type BrowserFetch struct {
	Timeout time.Duration
	Proxy   proxy.Func
	Logger  *zap.Logger
}

// Get 模拟浏览器访问
func (b *BrowserFetch) Get(request *Request) ([]byte, error) {

	client := &http.Client{
		Timeout: b.Timeout,
	}
	if b.Proxy != nil {
		transport := http.DefaultTransport.(*http.Transport)
		transport.Proxy = b.Proxy
		client.Transport = transport
	}
	req, err := http.NewRequest("GET", request.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("get url failed:%v", err)
	}
	// 验证cookie
	if len(request.Task.Cookie) > 0 {
		req.Header.Set("Cookie", request.Task.Cookie)
	}
	req.Header.Set("User-Agent", extension.GenerateRandomUA())
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	bodyReader := bufio.NewReader(resp.Body)
	e := DetermineEncoding(bodyReader)
	utf8Reader := transform.NewReader(bodyReader, e.NewDecoder())
	return io.ReadAll(utf8Reader)
}

func DetermineEncoding(r *bufio.Reader) encoding.Encoding {

	bytes, err := r.Peek(1024)

	if err != nil {
		zap.L().Error("fetch failed", zap.Error(err))
		return unicode.UTF8
	}

	e, _, _ := charset.DetermineEncoding(bytes, "")
	return e
}
