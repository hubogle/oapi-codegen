# OpenApi Gin

通过 `OpenApi` 生成 `Gin` 代码，以及项目的基础框架。

* 基于 [OpenAPI 3.0](https://github.com/OAI/OpenAPI-Specification/blob/main/versions/3.0.0.md) 定义的服务生成 `Go` 代码。
* 基于 `Gin` 作为默认的 `HTTP` 路由引擎。
* 无法将所有的 `OpenAPI` 模式生成强类型 `Go` 代码，但是可以生成大部分的模式。

依赖于 [kin-openapi](https://github.com/getkin/kin-openapi) 项目，最新版只支持 `OpenAPI 3.0`。
