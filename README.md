# xzitpocket-control

用于 xzitpocket 的管理、用户、付费服务鉴权和 OAuth 控制中心。

## 特性

- Go 模块化单体服务；
- SQLite WAL；
- 客户端 OA 登录仍完全在客户端完成；
- 设备 P-256 签名、一次性 challenge 和短期 control 会话；
- 多设备用户关系；
- 付费服务短期 Ed25519 JWT 和 JWKS；
- APP 活跃统计和日聚合；
- 只记录不处置的风控事件；
- Vue 3 + ECharts CDN 管理后台；
- OAuth Authorization Server 和外部 OAuth Client 基础流程。

control 不接收 OA 密码、Cookie、TGT、Service Ticket 或 OA 响应，也不会访问 CAS/教务系统。

## 本地运行

需要 Go 1.22+。

~~~powershell
$env:CONTROL_ADMIN_BOOTSTRAP = "replace-me"
go run ./cmd/server
~~~

默认地址：http://127.0.0.1:8080

首次启动会创建 SQLite 数据库、迁移表结构、服务端 Ed25519 签名密钥和管理员账号。

## 常用检查

~~~powershell
go test ./...
go vet ./...
~~~

生产环境必须设置独立的 CONTROL_TOKEN_PEPPER、CONTROL_ID_PEPPER、CONTROL_ENCRYPTION_KEY 和管理员初始化口令，并通过 HTTPS 反向代理暴露服务。
