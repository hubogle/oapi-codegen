# OpenApi Gin

通过 `OpenApi` 生成 `Gin` 代码，以及项目的基础框架。

* 基于 [OpenAPI 3.0](https://github.com/OAI/OpenAPI-Specification/blob/main/versions/3.0.0.md) 定义的服务生成 `Go` 代码。
* 基于 `Gin` 作为默认的 `HTTP` 路由引擎。
* 无法将所有的 `OpenAPI` 模式生成强类型 `Go` 代码，但是可以生成大部分的模式。

依赖于 [kin-openapi](https://github.com/getkin/kin-openapi) 项目，最新版只支持 `OpenAPI 3.0`。

## 支持功能
`openapi` 定义规则：
1. `x-middleware: "auth,rate_limit"` 该 `tag` 定义会生成中间件
2. `x-group: admin` 该 `tag` 将多个 `path` 定义为同一个 `group`

### 字段类型定义

```yaml
application/json:
  schema:
    properties:
      map:
        type: object
        description: map 类型定义
        additionalProperties:
        type: integer
      array:
        type: array
        description: 二维 int 数组
        items:
        type: array
        items:
          type: integer
      arrayObj:
        type: array
        description: 数组对象定义
        items:
        type: object
        properties:
          key:
          type: integer
          value:
          type: integer
```
```golang
	// Array 二维 int 数组
	Array *[][]int `json:"array,omitempty"`

	// ArrayObj 数组对象定义
	ArrayObj *[]struct {
		Key   *int `json:"key,omitempty"`
		Value *int `json:"value,omitempty"`
	} `json:"arrayObj,omitempty"`

	// Map map 类型定义
	Map *map[string]int `json:"map,omitempty"`
```

#### 上传文件定义
1. `multipart/form-data` 格式上传文件，可以在请求体中包含多个参数，其中一个参数为文件。
```yaml
multipart/form-data:
  schema:
    type: object
    properties:
      file:
        type: string
        format: binary
```

```golang
File *multipart.FileHeader `json:"file,omitempty"`
```
