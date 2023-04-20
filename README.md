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

`properties` 定义规则：
1. `x-go-type`: 指定 `Go` 的类型名称。
2. `x-go-name`: 指定 `Go` 的字段名称。
```yaml
schema:
  type: object
  properties:
    name:
      format: string
      type: string
      x-go-type: int
      x-go-name: UserName
```

```golang
// CreateUserJSONBody defines parameters for CreateUser.
type CreateUserJSONBody struct {
	UserName *int `json:"name,omitempty"`
}
```
3. `x-validate`: 自定义 `validate` 的校验 `tag`
```yaml
schema:
  type: object
  properties:
    name:
      format: string
      type: string
      default: "admin"
      x-validate: "required,gte=5"
```

```golang
// CreateUserJSONBody defines parameters for CreateUser.
type CreateUserJSONBody struct {
	Name *string `json:"name,omitempty" validate:"required,gte=5"`
}
```
4. `x-enum-varnames`: 自定义枚举 `enum` 变量对应的名称，方便调用。
```yaml
schema:
  type: object
  properties:
    name:
      format: string
      type: string
      enum:
        - admin
        - openapi
      x-enum-varnames:
        - AdminUser
        - OpenAPIUser
```

```golang
// CreateUserJSONBodyName defines parameters for CreateUser.
type CreateUserJSONBodyName string

// Defines values for CreateUserJSONBodyName.
const (
	AdminUser   CreateUserJSONBodyName = "admin"
	OpenAPIUser CreateUserJSONBodyName = "openapi"
)

// CreateUserJSONBody defines parameters for CreateUser.
type CreateUserJSONBody struct {
	Name *CreateUserJSONBodyName `json:"name,omitempty" validate:"oneof=openapi admin"`
}
```
5. `x-oapi-codegen-extra-tags`: 向生成的结构字段添加额外的 Go 字段标签。
```yaml
schema:
  type: object
  properties:
    name:
      format: string
      type: string
      x-oapi-codegen-extra-tags:
        gorm: name
```

```golang
// CreateUserJSONBody defines parameters for CreateUser.
type CreateUserJSONBody struct {
	Name *string `gorm:"name" json:"name,omitempty"`
}
```

6. `x-go-type-import`: 将额外的 `Go` 导入添加到生成的代码中，配合 `x-go-type`使用。
```yaml
schema:
  type: object
  properties:
    name:
      format: string
      type: string
      x-go-type-import:
        path: mime/multipart
      x-go-type: "multipart.FileHeader"
```

```golang
import (
	"mime/multipart"
)

// CreateUserJSONBody defines parameters for CreateUser.
type CreateUserJSONBody struct {
	Name *multipart.FileHeader `json:"name,omitempty"`
}
```

### 字段 tag 定义
* array
  1. `uniqueItems` 对应 `validator` 中的 `unique` 用于约束数组中没有重复元素，`map` 用于约束没重复的值。
  2. `minItems,maxItems` 校验数组元素的最小\最大数量
  3. `items` 校验数组内的元素
* integer, number
  1. `exclusiveMinimum` 用于指定数字类型属性的最小值，当为 `true` 表示最小值不包含该值。对应 `validator` 中的 `gt` 或 `gte`。
  2. `exclusiveMaximum` 与 `exclusiveMinimum` 同理，对应 `validator` 中的 `lt` 或 `lte`。
  3. `minimum,maximum` 数值的最大最小值
* string
  1. `minLength,maxLength` 字符串最短和最长值

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
