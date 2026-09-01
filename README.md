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

需要 Go 1.23+（`modernc.org/sqlite` 当前版本要求）。

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

## API 快速索引

客户端只需要上传 OA 登录完成后的 `student_id`、固定算法生成的 `student_alias` 和设备断言；control 从不接收 OA 密码、Cookie 或 Ticket。

- `POST /api/v1/devices/register`：注册安装并取得 `device_serial`、`device_token`；
- `POST /api/v1/auth/challenges`、`POST /api/v1/auth/assertions`：完成 control 用户会话；
- `POST /api/v1/auth/refresh`、`POST /api/v1/auth/revoke`：会话轮换和退出；
- `POST /api/v1/telemetry/events`：批量上报允许的活跃事件；
- `POST /api/v1/services/{service}/tokens`：为文库等付费服务签发短期 JWT；
- `GET /.well-known/jwks.json`：下游服务验证 JWT 的公钥；
- `/oauth/authorize`、`/oauth/token`、`/oauth/userinfo`、`/oauth/revoke`：Authorization Code + PKCE；
- `/api/v1/admin/*`：管理员统计、用户、设备、风控、OAuth 和审计接口。

统计接口包括 `/api/v1/admin/metrics/overview`、`/api/v1/admin/metrics/series` 和 `/api/v1/admin/metrics/breakdown`。

设备签名原文、固定伪名算法和付费设备绑定边界以 [`fmd/system-design.md`](fmd/system-design.md) 为准。风控只记录短时间登录尝试和设备数量异常，不会封禁、解绑、撤销令牌或阻塞任何服务。
