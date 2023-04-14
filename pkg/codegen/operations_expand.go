package codegen

import (
	"text/template"
)

func GenerateGinSvc(t *template.Template) (string, error) {
	return GenerateTemplates([]string{"gin/gin-svc.tmpl"}, t, nil)
}

func GenerateGinCode(t *template.Template) (string, error) {
	return GenerateTemplates([]string{"gin/gin-code.tmpl"}, t, nil)
}

type GinOperation struct {
	OperationDefinition
	PkgName       string // 按 group 分组的名称
	ShouldBindStr string // 绑定参数字段信息
	ImportPkgName string // 主要有 svc、type 两个包的路径
	ReqName       string // 请求 req 参数名称，如：user.UserRequest
	RespName      string // 返回 resp 参数名称，如：user.UserResponse
}

// GenerateGinResponse
func GenerateGinResponse(t *template.Template, operations *GinOperation) (string, error) {
	return GenerateTemplates([]string{"gin/gin-response.tmpl"}, t, operations)
}

// GenerateGinHandler 生成 handler
func GenerateGinHandler(t *template.Template, operations *GinOperation) (string, error) {
	return GenerateTemplates([]string{"gin/gin-handler.tmpl"}, t, operations)
}

// GenerateGinLogic 生成 logic
func GenerateGinLogic(t *template.Template, operations *GinOperation) (string, error) {
	return GenerateTemplates([]string{"gin/gin-logic.tmpl"}, t, operations)
}

type GinRoutesOperation struct {
	Ops           []OperationDefinition
	PkgName       string   // 按 group 分组的名称
	ImportPkgName string   // 主要有 svc、type 两个包的路径
	Middlewares   []string // 中间件列表
	GroupNameList []string // route 下的 group 组
}

// GenerateGinRoutes 生成 routes
func GenerateGinRoutes(t *template.Template, operations *GinRoutesOperation) (string, error) {
	return GenerateTemplates([]string{"gin/gin-routes.tmpl"}, t, operations)
}

// GenerateGinRoutesSetup 生成 routes
func GenerateGinRoutesSetup(t *template.Template, operations *GinRoutesOperation) (string, error) {
	return GenerateTemplates([]string{"gin/gin-routes-setup.tmpl"}, t, operations)
}

// GenerateGinMiddleware 生成 middleware
func GenerateGinMiddleware(t *template.Template, name string) (string, error) {
	return GenerateTemplates([]string{"gin/gin-middleware.tmpl"}, t, name)
}
