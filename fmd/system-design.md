# 掌上徐工控制台系统设计

## 1. 定位与边界

control 是 Go + Vue CDN + Vuetify 4 + SQLite 的模块化单体，面向 xzitpocket 提供设备登记、用户会话、活跃统计、风控记录、审计、APP 发布配置和内置文库。

OA 登录由 xzitpocket 客户端完成。control 不读取、转发或保存 OA 密码、Cookie、Ticket 或 OA 响应，只接收客户端完成登录后提交的学号、显示名、固定伪名和设备断言。

control 不可用时，不影响 OA 登录、APP 启动和免费功能；只影响 control 相关的新会话、统计上报和受保护题库读取。

## 2. 架构

```text
xzitpocket 客户端
  ├─ 直接访问 OA
  ├─ 保存 OA 登录结果
  └─ 上传学号、伪名和设备断言
          │
          ▼
control 单体
  ├─ httpapi：路由、认证、请求校验
  ├─ app：身份、会话、统计、发布配置、文库、风控
  ├─ store/sqlite：事务和查询
  └─ web：嵌入式 Vue 管理后台
          │
          ▼
      SQLite（WAL）
```

新增功能沿用 `httpapi -> app -> store/sqlite` 三层，不拆分微服务，不引入 Redis 或消息队列。

## 3. 身份与会话

### 3.1 设备

客户端为每次安装生成 `installation_id` 和 P-256 密钥对，并签名：

```text
xzitpocket-control-device
installation_id
public_key
platform
app_version
created_at
```

control 校验签名和时间窗口后生成 `device_id`、`device_serial` 和随机 `device_token`。数据库只保存令牌 HMAC 和公钥，不保存私钥。

`installation_id` 是安装实例标识；卸载重装或重新生成安装标识会产生新的设备记录。`device_serial` 只用于设备识别、会话关联和风控展示，不作为文库业务身份。

### 3.2 登录

客户端先直接完成 OA 登录，再请求一次性 challenge。断言签名原文为：

```text
xzitpocket-control-login
challenge_id
challenge
device_serial
student_id
student_alias
display_name
asserted_at
```

control 验证设备签名、challenge 一次性使用、学号格式和伪名后，按学号哈希查找或创建用户，绑定当前设备并创建会话。同一学号可在多台设备登录；设备切换账号只撤销该设备在旧账号上的会话。

伪名固定为 `SHA-256("xzitpocket-control|student|" + student_id)` 的小写十六进制字符串，不带版本号。access token 有效期 15 分钟，refresh token 有效期 30 天，刷新时轮换；数据库只保存 HMAC。

## 4. 内置文库

### 4.1 模型

| 表 | 用途 |
| --- | --- |
| `question_banks` | 题库 ID、顺序 ID、名称、状态和 CDK 解锁开关 |
| `questions` | 题号、题型、题干和答案 |
| `question_options` | 选择题选项 |
| `question_bank_id_counter` | 事务内单调分配 `QB-001` 等 ID |
| `library_cdks` | 单题库、单次使用的 CDK |

题库状态只有 `active`、`draft`、`disabled`。创建时客户端不能指定题库 ID，control 自动生成且不复用；`orderId` 是正整数，用于控制 xzitpocket 展示顺序。未填写时按顺序计数器自动分配，管理员可编辑为未占用的顺序 ID；停用只是改为 `disabled`，管理员列表仍显示。

题型固定为 `单选题`、`多选题`、`判断题`、`填空题`。服务端校验题号唯一、选项标签唯一及答案引用关系。

### 4.2 JSON

用户读取 `GET /api/v1/question-banks/{id}`，成功响应为：

```json
{
  "questionBank": {
    "id": "QB-001",
    "orderId": 1,
    "new": true,
    "name": "计算机基础知识测验",
    "requiresCDK": false,
    "questions": [
      {
        "questionNumber": 1,
        "type": "单选题",
        "title": "第1题",
        "questionText": "以下哪个是计算机的核心部件？",
        "options": [
          {"label": "A", "text": "显示器"},
          {"label": "B", "text": "CPU"}
        ],
        "correctAnswer": "B"
      }
    ]
  }
}
```

多选答案为逗号分隔标签，填空题 `options` 必须为空。受保护题库要求当前学号已兑换对应 CDK，否则返回 `question_bank_locked`。

### 4.3 CDK

管理员通过 `POST /api/v1/admin/library-cdks` 生成 CDK，可用 `count` 批量生成，单次最多 500 个。CDK 明文只在创建响应中返回一次，数据库只保存哈希。首次兑换绑定学号，同一学号重复兑换幂等，其他学号拒绝；同一学号换设备仍可访问。

## 5. 活跃统计、风控与审计

允许事件：`app_start`、`foreground`、`heartbeat`、`control_login_success`、`logout`、`library_open`。`POST /api/v1/telemetry/events` 接受事件数组；同一设备的 `event_id` 幂等。DAU/WAU/MAU 使用当天及近 7/30 个日历日去重统计，原始事件默认保留 90 天。

总览提供用户、设备、事件趋势，以及文库 CDK 今日、本周和累计成功兑换数。

风控只观察并列出两类异常：短时间登录尝试过多、账号绑定设备过多。达到阈值创建 `risk_events`，后台可标记已查看；不封禁、不解绑、不撤销会话，也不阻塞任何服务。重要写操作记录 `audit_logs`。

## 6. 管理 API

管理员登录：`POST /api/v1/admin/session`，请求体 `{"key":"..."}`。系统只有一个本地管理员，不显示用户名或角色；浏览器使用会话 Cookie 和 CSRF，脚本可使用 Bearer 管理员令牌。

| 方法 | 路径 |
| --- | --- |
| GET | `/admin/metrics/overview`、`/series`、`/breakdown` |
| GET | `/admin/users`、`/admin/users/{id}` |
| PATCH | `/admin/users/{id}/status` |
| GET | `/admin/devices`、`/admin/risk-events`、`/admin/audit` |
| PATCH | `/admin/risk-events/{id}` |
| GET/POST | `/admin/question-banks` |
| GET/PUT/DELETE | `/admin/question-banks/{id}` |
| GET/POST | `/admin/library-cdks` |
| PATCH | `/admin/library-cdks/{id}` |
| GET | `/app/release`（公开读取 APP 发布信息） |
| GET | `/admin/app/release` |
| PUT | `/admin/app/release` |
| POST/DELETE | `/admin/session` |

题库创建和更新只接受 `{ "questionBank": ... }`；创建不带题库 ID，更新通过 URL 指定 ID。`orderId` 可在创建或更新时设置，必须是未占用的正整数；省略时创建自动分配、更新保留原值。错误统一返回 `error.code`、`error.message` 和 `request_id`。

## 6.1 APP 发布配置

管理后台的“配置”页只维护当前 APP 的最新版版本号和下载 URL。配置保存在 `app_release_config` 单例表中，更新操作写入 `audit_logs`，动作名为 `app_release_update`。客户端未来可通过无需登录的 `GET /api/v1/app/release` 读取：

```json
{
  "latestVersion": "2.0.4",
  "downloadUrl": "https://example.com/xzitpocket.apk"
}
```

版本号必须以数字开头，下载地址必须是绝对 HTTP 或 HTTPS 地址；control 不负责托管安装包文件。

## 7. 配置与数据

程序只读取运行目录 `data/.env`，不读取系统环境变量。文件缺失时自动创建并生成随机密钥。

| 配置项 | 默认值 | 用途 |
| --- | --- | --- |
| `CONTROL_ADDR` | `127.0.0.1:8080` | 监听地址 |
| `CONTROL_DB` | `data/control.db` | SQLite 路径 |
| `CONTROL_PUBLIC_BASE_URL` | `http://127.0.0.1:8080` | 对外地址 |
| `CONTROL_TOKEN_PEPPER` | 自动生成 | 令牌 HMAC |
| `CONTROL_ID_PEPPER` | 自动生成 | 学号/IP HMAC |
| `CONTROL_ENCRYPTION_KEY` | 自动生成 | 学号加密 |
| `CONTROL_ADMIN_KEY` | `change-me` | 管理密钥 |
| `CONTROL_EVENT_RETENTION_DAYS` | `90` | 事件保留天数 |
| `CONTROL_RISK_LOGIN_WINDOW` | `10m` | 登录观察窗口 |
| `CONTROL_RISK_LOGIN_COUNT` | `5` | 登录观察阈值 |
| `CONTROL_RISK_DEVICE_COUNT` | `5` | 设备观察阈值 |

当前数据库唯一结构来源为 `internal/store/sqlite/schema.sql`，启动时直接创建表和索引。SQLite 开启外键、WAL 和 `busy_timeout=5000`。

## 8. 响应式后台

管理后台使用 Vuetify 4 CDN，桌面端固定侧栏，移动端临时抽屉；表格在窄屏横向滚动，工具栏和表单自动换行。导航使用纯色背景和明确的悬停、选中状态，不使用渐变。分页、空状态和辅助标签使用中文。

## 9. 验收标准

- OA 凭据不会进入 control；
- 同一学号可在多设备登录，设备签名不阻止其他设备；
- 伪名稳定且不带版本号；
- control 故障不影响 OA 和免费功能；
- 题库 JSON 字段稳定、题型和答案关系有效；
- CDK 按学号绑定、批量生成、可搜索且明文不落库；
- 题库 ID 自动生成且不重复，顺序 ID 可调整且不重复，停用后仍显示；
- 管理员可在用户列表直接手动停用用户，停用状态会阻止其会话继续使用；
- 风控只记录展示；
- `go test ./...`、`go vet ./...`、`node --check web/app.js` 和 `git diff --check` 通过。
