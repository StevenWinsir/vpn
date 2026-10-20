package authmail

import (
	"bytes"
	"errors"
	htmltemplate "html/template"
	"net/url"
	texttemplate "text/template"
)

// The three fixed server-side templates (handoff_auth.md 6.1).
const (
	TemplateRegisterCode    = "register_code"
	TemplateResetLink       = "reset_link"
	TemplatePasswordChanged = "password_changed"
)

const brand = "AsterLink VPN"

// mailPayload is the sealed content of one job. It never contains a password.
type mailPayload struct {
	To      string `json:"to"`
	Code    string `json:"code,omitempty"`
	Link    string `json:"link,omitempty"`
	Minutes int    `json:"minutes,omitempty"`
}

// Rendered is a ready-to-send message body.
type Rendered struct {
	Subject, Text, HTML string
}

type templateData struct {
	Brand, Code, Link, Site string
	Minutes                 int
}

var subjects = map[string]string{
	TemplateRegisterCode:    brand + " 注册验证码",
	TemplateResetLink:       brand + " 重置密码",
	TemplatePasswordChanged: brand + " 密码已修改",
}

var textTemplates = map[string]*texttemplate.Template{
	TemplateRegisterCode: texttemplate.Must(texttemplate.New("t").Parse(`您好：

您正在注册 {{.Brand}} 账户，本次邮箱验证码为：

    {{.Code}}

验证码 {{.Minutes}} 分钟内有效，仅可使用一次。如果您多次申请，请只使用最新一封邮件中的验证码。

如果这不是您本人的操作，请忽略本邮件，无需任何处理。请勿将验证码告诉任何人。

{{.Brand}}（{{.Site}}）
`)),
	TemplateResetLink: texttemplate.Must(texttemplate.New("t").Parse(`您好：

我们收到了重置 {{.Brand}} 账户密码的申请。请在 {{.Minutes}} 分钟内打开以下链接设置新密码（链接仅可使用一次）：

{{.Link}}

请确认链接属于 {{.Site}}。如果您多次申请，请只使用最新一封邮件中的链接。

如果这不是您本人的操作，请忽略本邮件，您的密码不会改变。

{{.Brand}}
`)),
	TemplatePasswordChanged: texttemplate.Must(texttemplate.New("t").Parse(`您好：

您的 {{.Brand}} 账户密码刚刚已被修改，所有设备上的原有登录均已退出，请使用新密码重新登录。

如果这不是您本人的操作，请立即通过 {{.Site}} 的“忘记密码”重新设置密码。

{{.Brand}}
`)),
}

const htmlLayout = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>{{.Subject}}</title></head>` +
	`<body style="margin:0;padding:24px;background:#f5f7fb;font-family:-apple-system,'PingFang SC','Microsoft YaHei',sans-serif;color:#1f2937">` +
	`<div style="max-width:520px;margin:0 auto;background:#ffffff;border-radius:12px;padding:28px">` +
	`<p style="margin:0 0 16px;font-size:18px;font-weight:600">{{.Data.Brand}}</p>{{template "body" .Data}}` +
	`<p style="margin:24px 0 0;font-size:12px;color:#6b7280">此邮件由 {{.Data.Site}} 自动发送，请勿直接回复。</p></div></body></html>`

var htmlTemplates = map[string]*htmltemplate.Template{
	TemplateRegisterCode: htmlBody(`{{define "body"}}<p>您正在注册 {{.Brand}} 账户，本次邮箱验证码为：</p>` +
		`<p style="font-size:30px;letter-spacing:8px;font-weight:700;margin:16px 0">{{.Code}}</p>` +
		`<p>验证码 {{.Minutes}} 分钟内有效，仅可使用一次。如果您多次申请，请只使用最新一封邮件中的验证码。</p>` +
		`<p style="color:#6b7280">如果这不是您本人的操作，请忽略本邮件。请勿将验证码告诉任何人。</p>{{end}}`),
	TemplateResetLink: htmlBody(`{{define "body"}}<p>我们收到了重置 {{.Brand}} 账户密码的申请。请在 {{.Minutes}} 分钟内点击下方按钮设置新密码（仅可使用一次）。</p>` +
		`<p style="margin:20px 0"><a href="{{.Link}}" style="display:inline-block;background:#2563eb;color:#ffffff;text-decoration:none;padding:10px 20px;border-radius:8px">设置新密码</a></p>` +
		`<p>按钮无法打开时，请复制以下链接到浏览器（请确认属于 {{.Site}}）：</p><p style="word-break:break-all;font-size:13px">{{.Link}}</p>` +
		`<p style="color:#6b7280">如果您多次申请，请只使用最新一封邮件中的链接。如果这不是您本人的操作，请忽略本邮件，您的密码不会改变。</p>{{end}}`),
	TemplatePasswordChanged: htmlBody(`{{define "body"}}<p>您的 {{.Brand}} 账户密码刚刚已被修改，所有设备上的原有登录均已退出，请使用新密码重新登录。</p>` +
		`<p style="color:#6b7280">如果这不是您本人的操作，请立即通过 {{.Site}} 的“忘记密码”重新设置密码。</p>{{end}}`),
}

func htmlBody(body string) *htmltemplate.Template {
	return htmltemplate.Must(htmltemplate.Must(htmltemplate.New("layout").Parse(htmlLayout)).Parse(body))
}

// Render produces the plain-text and HTML alternatives. html/template escapes every
// value; the link is checked to be an https/http URL on the configured website.
func Render(template string, p mailPayload, website string) (Rendered, error) {
	subject, ok := subjects[template]
	if !ok {
		return Rendered{}, errors.New("unknown mail template")
	}
	site, err := url.Parse(website)
	if err != nil || site.Host == "" {
		return Rendered{}, errors.New("invalid website address")
	}
	data := templateData{Brand: brand, Code: p.Code, Link: p.Link, Site: site.Host, Minutes: p.Minutes}
	switch template {
	case TemplateRegisterCode:
		if !ValidCodeFormat(p.Code) || p.Minutes <= 0 {
			return Rendered{}, errors.New("incomplete registration mail")
		}
	case TemplateResetLink:
		link, e := url.Parse(p.Link)
		if e != nil || link.Scheme != site.Scheme || link.Host != site.Host || p.Minutes <= 0 {
			return Rendered{}, errors.New("reset link is not on the configured website")
		}
	}
	var text, html bytes.Buffer
	if err = textTemplates[template].Execute(&text, data); err != nil {
		return Rendered{}, err
	}
	if err = htmlTemplates[template].Execute(&html, struct {
		Subject string
		Data    templateData
	}{subject, data}); err != nil {
		return Rendered{}, err
	}
	return Rendered{Subject: subject, Text: text.String(), HTML: html.String()}, nil
}
