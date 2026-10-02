<div align="center">

# XzitPocket Control - 掌上徐工控制台

[![License: GPL](https://img.shields.io/badge/License-GPLv3-yellow.svg)](https://opensource.org/licenses/gpl-3-0)

</div>

[掌上徐工控制台](https://github.com/lose2me/xzitpocket-control) 是一个开源、模块化的 Go + SQLite 单体服务，内置 Vue 3、Vuetify 4 和 ECharts 管理后台，为 [掌上徐工](https://github.com/lose2me/xzitpocket) 提供设备登记、用户会话、活跃统计、风控记录和内置文库。

> OA 登录始终由 xzitpocket 客户端完成。control 只接收客户端提交的学号、固定伪名和设备断言，不读取、不转发、不保存 OA 密码、Cookie、Ticket 或 OA 响应。

<div align="center">

***``不接触 OA 凭据`` ``永久开源``***

</div>

**功能 | 已实现**:
- [x] 设备登记与 P-256 设备签名校验
- [x] 用户会话、刷新与撤销
- [x] 同一学号多设备登录，按设备撤销旧账号会话
- [x] 活跃统计与 DAU / WAU / MAU 趋势
- [x] 风控记录（登录频繁、设备过多）
- [x] 错误上报归档、忽略与清空
- [x] 内置题库与 CDK 解锁
- [x] APP 发布与校历配置
- [x] 内置 Vue 3 管理后台
*以优先级排序*

## 运行

Go
```
go run ./cmd/server
```

需要 Go 1.23+。程序只从运行目录的 `data/.env` 读取配置，不读取系统环境变量；文件不存在时自动创建，并生成三个随机密钥。默认地址为 `http://127.0.0.1:8080`，管理后台和 API 同源。首次启动会创建 SQLite 数据库和唯一管理员，管理密钥由 `CONTROL_ADMIN_KEY` 设置，默认 `change-me`，部署前应修改。

配置模板见 [`data/.env.example`](data/.env.example)。`data/.env`、数据库和运行时密钥不会提交到仓库。

## 构建

Windows
```
go build -o dist/xzitpocket-control.exe ./cmd/server
```

Linux
```
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/xzitpocket-control-linux-amd64 ./cmd/server
```

## Release

Go
```
go vet ./... && go test ./...
```

管理后台前端资源通过 `go:embed` 打进二进制，无独立构建步骤；`dist/` 下的可执行文件随仓库发布。

## API

API 前缀为 `/api/v1`。设备登记使用 `Authorization: Device <device_token>`；用户会话和题库读取使用 `Authorization: Bearer <access_token>`；管理员登录使用 `POST /admin/session` 提交 `{"key":"..."}`。

客户端接口：

- `POST /devices/register`
- `POST /auth/challenges`、`POST /auth/assertions`、`POST /auth/refresh`、`POST /auth/revoke`
- `GET /me`、`GET /me/devices`
- `POST /telemetry/events`，请求体为事件数组
- `POST /error-reports`，仅接受已登录用户的错误日志，并按用户学号归档
- `GET /question-banks`、`GET /question-banks/{id}`
- `POST /library/cdks/redeem`，请求体为 `{"code":"CDK-...","question_bank_id":"QB-..."}`
- `GET /app/release`、`GET /school-calendar`、`GET /config/versions`
- `GET /healthz`

管理接口：

- `GET /admin/metrics/overview`、`/series`、`/breakdown`
- `GET /admin/users`、`GET /admin/users/{id}`、`PATCH /admin/users/{id}/status`
- `GET /admin/devices`、`PATCH /admin/devices/{id}/status`
- `GET/DELETE /admin/risk-events`、`PATCH /admin/risk-events/{id}`
- `GET/DELETE /admin/error-reports`、`PATCH /admin/error-reports/{id}`（`DELETE` 清空全部错误记录并恢复所有学号的错误上报；`PATCH` 请求体 `{"ignored":true}` 忽略该学号后续的错误上报，`false` 恢复接收）
- `GET/POST /admin/question-banks`、`GET/PUT/DELETE /admin/question-banks/{id}`、`PATCH /admin/question-banks/{id}/status`
- `GET/POST /admin/library-cdks`、`PATCH /admin/library-cdks/{id}`（请求体 `{"status":"active"}` 或 `{"status":"disabled"}`）
- `GET /admin/audit`
- `GET/PUT /admin/app/release`、`GET/PUT /admin/school-calendar`
- `POST/DELETE /admin/session`

错误统一返回 `error.code`、`error.message` 和 `request_id`。完整字段和设计说明见 [`fmd/system-design.md`](fmd/system-design.md)。

## 题库与 CDK

题库创建和更新只接受 `{ "questionBank": ... }`。创建时不提交题库 ID，服务端按 `QB-001`、`QB-002` 顺序生成且不复用；可选的 `orderId` 用于控制 xzitpocket 展示顺序，未填写时自动分配，已使用的顺序 ID 会被拒绝。更新通过 URL 指定 ID。题库状态为 `active`、`draft` 或 `disabled`，停用后仍保留在管理员列表。题型固定为 `单选题`、`多选题`、`判断题`、`填空题`。

题库可设置 `requiresCDK: true`。通用 CDK 创建时不指定题库，用户兑换时选择一个启用中的受保护题库；兑换成功后该 CDK 只解锁这个题库，并绑定当前学号，同一学号可在其他设备继续使用，一个已绑定的 CDK 不能再次兑换到其他题库。CDK 为 `CDK-` 加 16 位短码，明文只在创建响应中返回一次，数据库只保存哈希。CDK 支持启用、已兑换、禁用三种状态；禁用不清除绑定，管理员重新启用后恢复原题库权限。管理员支持批量生成和按 CDK、状态、题库、学号搜索。

## 数据和结构

当前数据库结构唯一来源是 [`internal/store/sqlite/schema.sql`](internal/store/sqlite/schema.sql)，启动时直接创建表和索引。SQLite 使用外键、WAL 和忙等待。所有题库写入在事务内完成，重要操作写入审计日志。

```text
xzitpocket-control/
├─ cmd/server/
├─ internal/analytics/
├─ internal/app/
├─ internal/audit/
├─ internal/config/
├─ internal/crypto/
├─ internal/httpapi/
├─ internal/identity/
├─ internal/oauth/
├─ internal/paid/
├─ internal/risk/
├─ internal/store/sqlite/
├─ internal/telemetry/
├─ web/
├─ data/
├─ fmd/system-design.md
└─ dist/
```

风控只记录两类异常：短时间登录次数过多、账号绑定设备过多。后台可查看和标记记录，但风控不会封禁、解绑或阻塞正常服务。control 不可用时不影响 OA 登录、APP 启动和免费功能。

管理后台时间统一显示为北京时间。用户“最近连接”和设备“最近活动”会在成功访问 control 会话接口时更新；活跃设备按最近 72 小时内是否有事件判断。近 30 天趋势显示累计总用户、DAU、WAU 和每日事件数；DAU/WAU 按北京时间自然日统计 `app_start`、`foreground`、`heartbeat`、`control_login_success`、`library_open` 等活跃事件，事件数保留全部已接收事件。

## 开发检查

```powershell
go test ./...
go vet ./...
node --check web/app.js
git diff --check
```
