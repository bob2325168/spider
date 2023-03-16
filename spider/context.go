package spider

import (
	"regexp"
	"time"
)

type Context struct {
	Req  *Request
	Body []byte
}

type ParseResult struct {
	Requests []*Request
	Items    []interface{}
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
				Method: "GET",
				Task:   ctx.Req.Task,
				URL:    u,
				//每搜索一次，深度加1
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

func (ctx *Context) Output(data interface{}) *DataCell {
	res := &DataCell{
		Task: ctx.Req.Task,
	}
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
