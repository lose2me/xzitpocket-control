# xzitpocket-control 系统方案

版本：v1.0  
状态：当前实施方案  
技术栈：Go + Vue 3 CDN + SQLite

## 1. 目标与边界

xzitpocket-control 是 xzitpocket 的管理与付费服务鉴权中心，第一阶段提供：

1. APP 活跃统计；
2. 用户系统；
3. 对接付费文库及其他系统的 OAuth；
4. 设备码管理；
5. 只记录、不自动处置的风控事件。

系统必须遵守以下边界：

- xzitpocket 继续在客户端直接登录 OA/CAS 和教务系统。
- control 可以接收客户端登录成功后上传的学号、姓名和客户端断言。
- control 不接收 OA 密码、Cookie、TGT、Service Ticket、execution、RSA 参数或 OA 响应正文。
- control 不访问 CAS、教务系统或其他 OA 接口。
- control 的用户身份仍然是客户端间接断言，不能称为 OA 官方验证身份。
- 从用户视角看，control 故障时只能让付费功能暂不可用；账号同步和统计在后台静默停止，不得成为 xzitpocket 免费功能的前置依赖。

## 2. 已确认的产品决策

### 2.1 学号和固定伪名

客户端登录 OA 成功后，向 control 上传：

- student_id：原始学号；
- student_alias：按本方案固定算法生成的伪名；
- display_name：姓名，可选；
- device_serial：control 分配的设备码；
- assertion：本次登录断言。

student_alias 算法永久固定，不携带算法版本号：

~~~text
student_id 必须匹配 ^[A-Za-z0-9._-]{1,64}$

student_alias =
  lowercase_hex(
    SHA-256(
      UTF-8("xzitpocket-control|student|" + student_id)
    )
  )
~~~

输出必须是 64 个 ASCII 小写十六进制字符。

固定测试向量：

~~~text
student_id    = 2023000001
student_alias = 7ca34e3c9fb519103feabf9c0e1954112556e0be0ed2656a731ea318f0cafbe3
~~~

服务端收到请求后必须重新计算并比对 student_alias。student_alias 是公开、稳定的标识，不是密码，也不能用于证明用户确实登录过 OA。

数据库使用以下两个值：

- student_id_hash = HMAC-SHA-256(CONTROL_ID_PEPPER, student_id)，用于唯一索引；
- student_id_ciphertext = AES-GCM(CONTROL_ENCRYPTION_KEY, student_id)，用于确实需要时恢复学号。

原始学号不得写入普通日志、风控 detail、埋点或 OAuth state。

### 2.2 多设备登录

设备密钥签名不会阻止同一账号登录其他设备。

规则如下：

- 每台设备独立生成自己的密钥对；
- 每次安装获得独立的 device_serial；
- 同一个 student_id 可以关联多个 device_serial；
- 用户在新设备登录时，不需要导入旧设备密钥；
- 新设备完成自己的注册和登录断言后，关联到同一个 control 用户；
- control 不设置自动设备数量上限，也不会因为设备较多拒绝登录。

数据关系是：

~~~text
user 1 ----- N user_devices N ----- 1 device
~~~

设备密钥只证明“这次请求来自这一个已注册设备”，不是整个账号只能使用这一把密钥。

### 2.3 设备码

device_serial 是 control 生成的随机设备码，不读取手机硬件序列号、IMEI、Android ID 或其他硬件标识。

建议格式：

~~~text
dev_<128 bit random encoded as lowercase base32>
~~~

要求：

- 由服务端生成，客户端不能自行指定；
- 全局唯一；
- 对同一次 APP 安装保持稳定；
- 不是秘密，可以传递给付费文库系统；
- 必须和设备公钥绑定，客户端不能冒用其他设备的 device_serial；
- APP 卸载且本地密钥丢失后，重新安装会得到新的 device_serial；
- 设备更换或重装导致 device_serial 变化时，付费文库走换绑流程。

### 2.4 付费服务故障隔离

control 主要用于付费服务鉴权。control 不可用时：

- xzitpocket 仍然可以登录 OA；
- 课表、考试、成绩、一卡通、报修、网络管理等现有免费功能继续工作；
- 本地缓存、小组件和本地设置继续工作；
- 活跃事件可以暂存后补发，也可以丢弃；
- 已签发且尚未过期的付费服务令牌可以继续使用；
- 新的付费令牌、设备绑定和换绑暂时不可用；
- APP 只在付费功能入口显示“服务暂不可用”，不得全局退出登录或阻塞启动。

APP 可以把尚未过期的付费 JWT 保存在系统安全存储中；文库必须本地验证 JWT，不为每次阅读请求同步回调 control。令牌过期且 control 仍不可用时，只有新的付费访问暂不可用。

control 调用不得放进 AuthService.loginAndFetchAll 的成功条件。客户端必须先完成现有本地登录、课表写入和状态更新，再异步同步 control；付费功能也可以在用户第一次进入付费页面时再完成 control 登录。

## 3. 当前 xzitpocket 登录方式

xzitpocket 当前登录流程为：

1. Profile 页面收集学号和密码；
2. AuthNotifier 调用 AuthService.loginAndFetchAll；
3. CasService.loginJw 优先尝试 CAS REST；
4. REST 不可用时，回退到带 execution 和 RSA 密码处理的 HTML CAS 登录；
5. 客户端访问教务系统 SSO；
6. 课表和考试接口能够正常返回并解析后，客户端认为登录成功；
7. 密码仅保存在客户端 FlutterSecureStorage 中，供后续校园服务直接登录。

对应代码：

- lib/providers/auth_provider.dart；
- lib/services/auth_service.dart；
- lib/services/cas_service.dart；
- lib/pages/profile/profile_page.dart；
- lib/services/credential_storage.dart。

control 接入不改变上述 CAS 逻辑，也不读取或上传 CredentialStorage 中的密码。

## 4. 简化鉴权方案

### 4.1 采用的防护

只保留以下适量防护：

- 全站 HTTPS，control 客户端严格校验证书；
- 每个安装使用随机 device_token；
- 每台设备使用独立 P-256 密钥对；
- 登录断言使用一次性 challenge；
- 登录断言由设备私钥签名；
- control access token 短期有效；
- refresh token 轮换并只保存哈希；
- device_serial 由服务端生成并绑定公钥；
- API 限流、请求去重、审计和风控记录；
- 付费服务令牌由 control 服务端签名，客户端不能修改其中的 device_serial。

本方案不使用平台完整性证明、证书锁定、root/jailbreak 自动拦截、客户端内置静态 secret 或复杂设备可信等级。客户端代码混淆只能用于提高分析成本，不能作为鉴权依据。

项目是开源的，因此客户端内置 secret、算法隐藏和代码混淆都不能形成可靠安全边界。

### 4.2 设备注册

客户端首次需要 control 时：

1. 生成随机 installation_id；
2. 在 Android Keystore、iOS Secure Enclave 或系统密钥库中生成不可导出的 P-256 密钥；
3. 导出公钥；
4. 用私钥签名注册内容；
5. 调用设备注册接口；
6. 服务端验签并生成 device_serial 和 device_token；
7. 客户端保存 installation_id、device_serial、device_token 和私钥引用。

注册签名原文固定为：

~~~text
xzitpocket-control-device
installation_id
public_key
platform
app_version
created_at
~~~

字段使用 UTF-8 和 LF 换行，字段内容禁止 LF、CR 和 NUL。

签名算法固定为：

- ECDSA P-256 + SHA-256；
- 签名编码为 ASN.1 DER；
- API 中使用 base64url，无 padding；
- Android 使用 SHA256withECDSA；
- Go 使用 ecdsa.VerifyASN1。

设备注册是开放接口，因此仍需按 IP 和 installation_id 限流。同一个 installation_id 不能覆盖已有公钥；重复注册应返回已有设备状态或 409，不得重新生成并替换 device_serial。设备签名证明客户端持有对应私钥，但不能证明这是未修改的官方客户端。

### 4.3 登录 challenge 和断言

客户端只有在本地 OA 登录成功后才提交 control 断言：

1. 使用 device_token 获取一次性 challenge；
2. 组织 student_id、student_alias、device_serial 和登录时间；
3. 使用设备私钥签名；
4. 提交 assertion；
5. 服务端验签、消费 challenge、查找或创建用户；
6. 建立 user_device 关系；
7. 返回 control access token 和 refresh token。

断言签名原文固定为：

~~~text
xzitpocket-control-login
challenge_id
challenge
device_serial
student_id
student_alias
display_name
asserted_at
~~~

display_name 允许为空，但该行必须保留。challenge 为 32 字节随机数的 base64url 表达，5 分钟有效，只能成功使用一次。

服务端必须校验：

- device_token 有效；
- device_serial 属于该 device_token；
- challenge 属于该设备、未过期、未使用；
- asserted_at 与服务端时间偏差不超过 10 分钟；
- student_id 格式正确；
- student_alias 重新计算后一致；
- 设备签名有效。

设备签名能够防止复制其他设备的 device_serial、篡改断言和重放已消费 challenge，但无法阻止修改过的开源客户端自行注册新设备并伪造“登录成功”。这是无法回调 OA、服务端不接触 OA 凭据时的固有限制。

### 4.4 control 会话

- access token：随机不透明令牌，15 分钟有效；
- refresh token：随机不透明令牌，30 天有效；
- 数据库只保存 token 的 HMAC 或哈希；
- refresh token 每次使用后轮换，旧 token 立即失效；
- session 同时绑定 user_id 和 device_id；
- refresh 请求必须由该 session 对应的设备私钥签名，复制 refresh token 不能在另一台设备使用；
- 切换学号时撤销该设备的旧 control session；
- control 会话退出不代表 OA 退出；
- OA 退出必须清除当前 control 用户会话，但不删除设备注册。

refresh 签名原文固定为：

~~~text
xzitpocket-control-refresh
device_serial
refresh_token
signed_at
~~~

服务端校验签名、X-Device-Signed-At 的 10 分钟时间窗以及 refresh token 与 device_id 的绑定，然后在一个事务中轮换 refresh token。

## 5. 付费文库鉴权与设备绑定

### 5.1 基本原则

control 负责向付费文库证明：

- 当前 control 用户 ID；
- student_alias；
- 当前 device_serial；
- 请求的服务和 scope；
- 令牌有效期。

这里的 device_serial 是付费文库绑定所需的 control 设备码，不是手机硬件序列号。

付费文库负责：

- 订单和付费权益；
- 首次设备绑定；
- 当前已绑定的 device_serial；
- 可绑定设备数量；
- 换绑确认和业务规则。

control 不根据客户端断言直接判定“该学号已经付费”。付费权益不能只依赖 student_id，因为客户端断言不是 OA 官方认证。

### 5.2 付费服务令牌

xzitpocket 使用 control access token和当前设备私钥签名请求文库服务令牌：

~~~http
POST /api/v1/services/document-library/tokens
Authorization: Bearer <control_access_token>
Content-Type: application/json

{
  "scope": ["library:read"]
}
~~~

control 从 session 中取得 device_id 和 device_serial。客户端请求体不能覆盖 device_serial。

服务令牌签名原文固定为：

~~~text
xzitpocket-control-service-token
lowercase_hex(SHA-256(access_token))
document-library
device_serial
library:read
X-Device-Signed-At
~~~

scope 先去重并按 ASCII 升序排列，再用单个空格连接。服务端校验签名、时间窗和 session.device_id 后才签发付费 JWT。这样即使 control token 被复制，也不能在没有设备私钥的环境中获得带原 device_serial 的新文库令牌。

返回一个由 control 独立 Ed25519 服务端私钥签名的短期 JWT（alg=EdDSA，kid 用于密钥轮换）：

~~~json
{
  "iss": "https://control.example.com",
  "aud": "document-library",
  "sub": "usr_01...",
  "student_alias": "64位小写十六进制",
  "device_serial": "dev_...",
  "scope": "library:read",
  "iat": 1788192000,
  "exp": 1788192600,
  "jti": "jti_..."
}
~~~

建议有效期为 10 分钟。文库通过 JWKS 获取 control 公钥，验证 alg、kid、签名、iss、aud、exp、jti 和 scope，不信任客户端自行提交的 device_serial。客户端设备 P-256 密钥和 control 服务端 Ed25519 签名密钥是两套独立密钥。

如文库确实需要原始学号，必须配置独立 scope，例如 student_id:read，并通过服务端到服务端接口获取；默认 JWT 不携带原始学号。

### 5.3 首次绑定

首次访问付费文库时：

1. APP 把付费服务令牌交给文库；
2. 文库验证令牌签名；
3. 文库读取 sub 和 device_serial；
4. 用户提交订单、激活码或文库已有的付费凭据；
5. 文库把权益绑定到 sub + device_serial；
6. 后续访问要求令牌中的 device_serial 命中绑定记录。

设备签名和服务端令牌使攻击者不能仅修改请求字段来冒用已经绑定的 device_serial。

### 5.4 换绑

新设备可以正常登录同一 control 账号，但其 device_serial 不同。文库发现设备不匹配时返回：

~~~json
{
  "error": {
    "code": "device_rebind_required",
    "message": "需要换绑设备"
  }
}
~~~

换绑由文库系统确认，建议支持以下任一方式：

- 原绑定设备确认；
- 订单或激活凭据再次验证；
- 文库系统人工处理。

control 只把新设备的可信 device_serial 传递给文库，不得仅凭相同 student_id 自动批准换绑，否则修改客户端可以伪造学号并抢占付费绑定。

文库换绑成功后，用新 device_serial 替换旧绑定。control 记录审计事件，但不决定文库的换绑次数或冷却时间。

### 5.5 多设备和付费设备数

- control 账号允许登录多台设备；
- 每台设备有独立 device_serial 和密钥；
- 付费文库可以允许一台或多台设备，这是文库自己的权益策略；
- 文库设备限制不会影响 control 登录；
- 文库设备限制不会影响 xzitpocket 免费功能。

## 6. 故障隔离

### 6.1 客户端调用顺序

正确顺序：

~~~text
客户端 OA 登录成功
        |
        +--> 保存课表、考试和本地用户状态
        |
        +--> 免费功能立即可用
        |
        +--> 异步同步 control
                  |
                  +--> 成功：可使用付费服务
                  |
                  +--> 失败：只禁用付费入口
~~~

禁止：

- 在 OA 登录前等待 control；
- control 失败后回滚本地 OA 登录；
- control token 失效后清除 OA 密码或本地课表；
- 在所有免费 API 请求前检查 control；
- 因统计上报失败弹出全局错误。

### 6.2 客户端代码边界

建议新增：

- lib/services/control_service.dart；
- lib/services/control_storage.dart；
- lib/services/control_device_key.dart；
- lib/services/control_event_queue.dart；
- lib/services/paid_service_auth.dart；
- Android 原生 control device key MethodChannel，接入现有 android/app/src/main/kotlin/live/xuda/xzitpocket/MainActivity.kt；
- device_token、refresh token 和私钥引用只写入系统安全存储，access token 只保存在内存；
- 以后需要时增加 iOS/macOS 对应实现。

control 不注入现有 CasService、AuthService 和各校园工具 Service。付费模块单独依赖 control。

## 7. 总体架构

采用模块化单体：

~~~text
xzitpocket
  |-- 直接访问 OA/CAS 和校园系统
  |
  |-- HTTPS
        |
        v
xzitpocket-control (Go)
  |-- identity
  |-- devices
  |-- sessions
  |-- telemetry
  |-- oauth
  |-- paid services
  |-- risk
  |-- audit
  |-- admin API
        |
        v
      SQLite

xzitpocket -- 付费 JWT --> 文库系统
文库系统   -- 验证公钥 --> xzitpocket-control JWKS
~~~

不拆微服务，不引入 Redis 和消息队列。Go 二进制同时提供 API 和嵌入的 Vue 管理后台。

推荐依赖：

- Go 1.22+；
- net/http ServeMux；
- database/sql；
- modernc.org/sqlite；
- golang.org/x/crypto/argon2；
- golang.org/x/oauth2；
- 成熟的 JWT/OAuth 库，不自行实现 JWT 签名解析。

## 8. SQLite 数据模型

### 8.1 核心表

| 表 | 关键字段 | 用途 |
| --- | --- | --- |
| users | id, status, display_name, created_at, last_login_at | control 用户 |
| identities | user_id, provider, student_id_hash, student_alias, student_id_ciphertext | 学号身份 |
| devices | id, device_serial, installation_id, token_hash, public_key, platform, app_version, created_at, revoked_at | 设备 |
| user_devices | user_id, device_id, first_bound_at, last_login_at, unbound_at | 多用户/多设备关系 |
| auth_challenges | id, device_id, challenge_hash, expires_at, used_at | 一次性登录 challenge |
| sessions | id, user_id, device_id, access_hash, refresh_hash, expires_at, refresh_expires_at, revoked_at | control 会话 |
| activity_events | id, event_id, user_id, device_id, type, occurred_at, received_at, properties_json | 活跃事件 |
| daily_metrics | day, app_id, metric, dimension_json, value | 日聚合 |
| oauth_clients | client_id, secret_hash, redirect_uris_json, scopes_json, status | OAuth 客户端 |
| oauth_codes | code_hash, client_id, user_id, device_id, scope, expires_at, used_at | OAuth 授权码 |
| oauth_tokens | token_hash, client_id, user_id, device_id, scope, expires_at, revoked_at | OAuth 令牌 |
| oauth_providers | id, name, authorization_url, token_url, client_id, secret_ciphertext, scopes_json, status | control 作为 OAuth Client 时的外部系统 |
| oauth_accounts | user_id, provider_id, external_subject, access_ciphertext, refresh_ciphertext, expires_at | 用户绑定的外部账号 |
| oauth_transactions | id, user_id, provider_id, state_hash, code_verifier_ciphertext, expires_at, used_at | OAuth state/PKCE 临时数据 |
| service_clients | id, audience, public_key_ciphertext/secret_ciphertext, scopes_json, status | 付费服务配置 |
| risk_events | id, type, user_id, device_id, observed_count, detail_json, created_at, acknowledged_at | 风控记录 |
| audit_logs | id, actor_id, action, target_type, target_id, detail_json, created_at | 审计 |
| schema_migrations | version, applied_at | 数据库迁移 |

### 8.2 索引

- UNIQUE(identities.provider, identities.student_id_hash)；
- UNIQUE(devices.device_serial)；
- UNIQUE(devices.installation_id)；
- UNIQUE(user_devices.user_id, user_devices.device_id)；
- UNIQUE(activity_events.device_id, activity_events.event_id)；
- activity_events(occurred_at, type)；
- sessions(user_id, device_id, revoked_at)；
- risk_events(created_at, type)。

### 8.3 SQLite 设置

- PRAGMA foreign_keys=ON；
- PRAGMA journal_mode=WAL；
- PRAGMA busy_timeout=5000；
- 写操作使用短事务；
- 定时备份数据库和服务端签名密钥；
- 原始事件默认保留 90 天；
- 聚合统计默认保留 24 个月。

## 9. API 设计

统一前缀：/api/v1。

错误格式：

~~~json
{
  "error": {
    "code": "challenge_expired",
    "message": "登录挑战已过期"
  },
  "request_id": "req_..."
}
~~~

鉴权方式：

- Authorization: Device <device_token>：设备注册完成后访问 challenge、assertion 和事件接口；
- Authorization: Bearer <access_token>：访问当前用户和付费服务接口；
- X-Device-Serial: <device_serial>：仅用于展示或排查，服务端以 session/device 映射为准，不能覆盖请求身份。

需要设备签名的请求统一使用以下请求头：

- X-Device-Signature：ECDSA P-256 + SHA-256，ASN.1 DER 后 base64url（无 padding）；
- X-Device-Signed-At：RFC3339 UTC，允许与服务端相差最多 10 分钟；
- X-Installation-ID：当前安装 ID。

签名原文由接口定义，签名字段不放入 JSON 请求体。服务端在解析 JSON 前限制请求体大小并验证签名；签名失败直接返回 401/403，不写入业务数据。

### 9.1 设备和用户

| 方法 | 路径 | 鉴权 | 用途 |
| --- | --- | --- | --- |
| POST | /api/v1/devices/register | 注册签名 | 注册设备，返回 device_serial 和 device_token |
| POST | /api/v1/auth/challenges | Device token | 获取一次性 challenge |
| POST | /api/v1/auth/assertions | Device token + 设备签名 | 提交学号断言 |
| POST | /api/v1/auth/refresh | Refresh token + 当前设备签名 | 轮换会话 |
| POST | /api/v1/auth/revoke | Bearer | 撤销 control 会话 |
| GET | /api/v1/me | Bearer | 当前用户和设备 |
| GET | /api/v1/me/devices | Bearer | 当前账号关联设备列表 |
| GET | /api/v1/me/oauth | Bearer | 已连接的外部 OAuth 账号 |
| POST | /api/v1/me/oauth/{provider}/start | Bearer | 创建外部 OAuth 授权地址 |
| DELETE | /api/v1/me/oauth/{provider} | Bearer | 解除外部 OAuth 账号 |
| GET | /api/v1/oauth/callback/{provider} | 浏览器回调 | 完成外部 OAuth code 交换 |

设备注册请求：

~~~json
{
  "installation_id": "inst_...",
  "public_key": "base64url DER SubjectPublicKeyInfo",
  "platform": "android",
  "app_version": "2.0.2",
  "created_at": "2026-09-01T00:00:00Z"
}
~~~

注册请求的签名放在 X-Device-Signature 请求头，签名原文按第 4.2 节固定规则计算；注册时使用请求体内的 public_key 验签。

设备注册响应：

~~~json
{
  "device_serial": "dev_...",
  "device_token": "随机令牌，只返回一次"
}
~~~

断言请求：

~~~json
{
  "challenge_id": "ch_...",
  "challenge": "base64url随机数",
  "device_serial": "dev_...",
  "student_id": "2023000001",
  "student_alias": "7ca34e3c9fb519103feabf9c0e1954112556e0be0ed2656a731ea318f0cafbe3",
  "display_name": "姓名",
  "asserted_at": "2026-09-01T00:01:00Z"
}
~~~

断言签名放在 X-Device-Signature 请求头，签名原文按第 4.3 节固定规则计算。

### 9.2 活跃统计

| 方法 | 路径 | 鉴权 | 用途 |
| --- | --- | --- | --- |
| POST | /api/v1/telemetry/events | Device token 或 Bearer | 批量上报事件 |
| GET | /api/v1/admin/metrics/overview | analyst/admin | DAU/WAU/MAU 总览 |
| GET | /api/v1/admin/metrics/series | analyst/admin | 时间序列 |

允许的基础事件：

- app_start；
- foreground；
- heartbeat；
- control_login_success；
- paid_service_open；
- paid_service_token_success；
- logout。

事件 properties 使用白名单，禁止上传学号、密码、Cookie、Ticket、完整 URL、请求正文和异常堆栈。

### 9.3 付费服务

| 方法 | 路径 | 鉴权 | 用途 |
| --- | --- | --- | --- |
| POST | /api/v1/services/{service}/tokens | Bearer + 当前设备签名 | 签发付费服务 JWT |
| GET | /.well-known/jwks.json | 无 | 下游服务获取验证公钥 |
| POST | /api/v1/services/{service}/introspect | Service client | 可选的令牌状态查询 |

### 9.4 管理后台

| 方法 | 路径 | 权限 | 用途 |
| --- | --- | --- | --- |
| POST | /api/v1/admin/session | 无 | 管理员登录 |
| DELETE | /api/v1/admin/session | analyst/admin | 退出 |
| GET | /api/v1/admin/users | analyst/admin | 用户列表 |
| GET | /api/v1/admin/users/{id} | analyst/admin | 用户及设备详情 |
| GET | /api/v1/admin/devices | analyst/admin | 设备列表 |
| GET | /api/v1/admin/risk-events | analyst/admin | 风控记录 |
| PATCH | /api/v1/admin/risk-events/{id} | analyst/admin | 标记已查看 |
| GET | /api/v1/admin/oauth-clients | admin | OAuth 客户端 |
| PUT | /api/v1/admin/oauth-clients/{id} | admin | 配置 OAuth 客户端 |
| GET | /api/v1/admin/oauth-providers | admin | 外部 OAuth Provider 配置 |
| PUT | /api/v1/admin/oauth-providers/{id} | admin | 配置外部 OAuth Provider |
| GET | /api/v1/admin/audit | analyst/admin | 审计日志 |

## 10. APP 活跃统计

定义：

- 活跃安装：当天至少上报一次 app_start、foreground 或 heartbeat 的 device_id；
- 活跃用户：上述事件已关联 user_id；
- DAU/WAU/MAU：按 1/7/30 天窗口去重 user_id；
- 匿名活跃：只有 device_id、没有 user_id 的事件；
- 新设备：当天首次注册的 device；
- 新用户：当天首次创建的 user；
- 版本分布：按事件发生时的 app_version；
- 付费入口使用量：paid_service_open 数量和去重用户数。

客户端策略：

- 冷启动上报 app_start；
- 回到前台上报 foreground；
- 前台每 15 分钟最多一次 heartbeat；
- 最多缓存 200 条或 7 天；
- 同一事件重试复用 event_id；
- control 不可用时不上报也不影响 APP。

## 11. 用户系统

用户状态：

- active；
- disabled，由管理员手动设置；
- deleted，匿名化后的软删除。

角色：

- user：APP 用户；
- analyst：只读统计和风控；
- admin：用户、OAuth、服务和系统配置。

管理员使用独立本地账号和 Argon2id 密码哈希，不使用 OA 密码。

同一 student_id 再次登录：

- 根据 student_id_hash 找到同一个 user；
- 更新 display_name 和 last_login_at；
- 更新或新增 user_devices；
- 不覆盖其他设备；
- 不因设备数量较多自动禁用用户。

## 12. OAuth 对接

control 第一阶段作为其他系统的 OAuth Authorization Server，至少支持：

- Authorization Code；
- PKCE；
- /oauth/authorize；
- /oauth/token；
- /oauth/userinfo；
- /oauth/revoke；
- /.well-known/jwks.json。

OAuth 对外默认只提供：

- sub：control user ID；
- student_alias；
- name；
- auth_source：client_assertion；
- scope。

原始 student_id 必须通过明确的 student_id:read scope 才能提供。
device_serial 只有在明确申请 device:read scope，或使用第 5 节付费文库 audience JWT 时才提供。

其他系统必须知道 auth_source=client_assertion 表示身份来自客户端间接断言，不得把它解释为 OA 官方认证。

付费文库 APP 内调用可以直接使用第 5 节的 audience JWT；浏览器或第三方 APP 使用 Authorization Code + PKCE。

必须使用成熟 OAuth/JWT 库实现授权码、PKCE、redirect URI 校验和签名验证。

### 12.1 作为 OAuth Client 对接外部系统

如果其他系统要求 control 主动登录其 OAuth Provider，增加独立的 OAuth Client 流程：

1. 管理员配置 provider 的 authorization_url、token_url、client_id、scope 和回调地址；
2. control 为每次连接生成随机 state 和 PKCE code_verifier，保存 5 分钟；
3. 用户从 control 会话进入授权地址；
4. 回调时严格校验 state、PKCE、redirect_uri 和 provider；
5. Go 服务端用 authorization code 换 token，并加密保存 access/refresh token；
6. 按 provider 返回的 external_subject 关联 oauth_account；
7. 前端只显示连接状态和外部账号名称，不返回 client_secret 或 refresh_token。

OAuth Client 的 token 只用于对应外部系统，不能提升 student_id 客户端断言的可信等级，也不与文库 audience JWT 混用。

## 13. 风控系统

风控只观察和记录，不封禁、不拒绝登录、不撤销 token、不限制设备，也不影响免费或付费服务。风控写入失败、统计写入失败或审计写入失败都不能回滚 OA 登录、control 登录或付费令牌签发；只记录服务端错误并在后台告警。

第一阶段只列出并记录两个规则：

### 13.1 短时间登录尝试多次

观察维度：

- 同一 user；
- 同一 student_id_hash；
- 同一 device；
- 同一来源 IP 的哈希。

每次提交 assertion 都算一次登录尝试，无论成功或失败；失败原因只保存粗粒度类别。记录 observed_count、观察窗口、关联用户和设备。阈值由配置决定，只产生 risk_event。

### 13.2 账号绑定太多设备码

当一个 user 关联的有效 device_serial 数量超过配置阈值时，产生 risk_event。

只在后台展示：

- 用户；
- 当前设备码数量；
- 首次和最近绑定时间；
- 关联设备列表；
- 是否已查看。

明确禁止自动动作：

- 不封禁账号；
- 不拒绝新设备登录；
- 不自动解绑；
- 不降低付费权限；
- 不影响服务响应；
- 不自动撤销会话。

管理员只能查看和标记“已查看”。risk_event 永远不调用用户禁用、设备解绑、token 撤销或付费权限接口；以后如需处置策略，必须单独设计并显式启用。

## 14. Vue 管理后台

Vue 3 通过固定版本 CDN 引入，Go 使用 embed.FS 提供静态文件。

页面：

- 管理员登录；
- 活跃总览；
- 趋势、平台和版本统计；
- 用户列表；
- 用户详情和设备码；
- 设备列表；
- 付费服务配置；
- OAuth 客户端；
- 风控事件；
- 审计日志；
- 系统状态。

后台与 API 同源部署，生产环境使用 HttpOnly、Secure、SameSite=Lax Cookie，并为写操作增加 CSRF token。

## 15. 目录结构

~~~text
xzitpocket-control/
├─ cmd/server/main.go
├─ internal/
│  ├─ config/
│  ├─ identity/
│  ├─ devices/
│  ├─ sessions/
│  ├─ telemetry/
│  ├─ analytics/
│  ├─ oauth/
│  ├─ paid/
│  ├─ risk/
│  ├─ audit/
│  ├─ store/sqlite/
│  └─ transport/http/
├─ migrations/
├─ web/
│  ├─ index.html
│  ├─ app.js
│  └─ styles.css
├─ data/
├─ fmd/
│  └─ system-design.md
├─ .env.example
├─ go.mod
├─ README.md
└─ Dockerfile
~~~

handler 不直接操作 sql.DB。各模块通过 repository 接口访问 SQLite，以便以后替换 PostgreSQL。

## 16. 配置

| 环境变量 | 用途 |
| --- | --- |
| CONTROL_ADDR | 监听地址 |
| CONTROL_DB | SQLite 路径 |
| CONTROL_TOKEN_PEPPER | token 哈希密钥 |
| CONTROL_ID_PEPPER | student_id_hash 密钥 |
| CONTROL_ENCRYPTION_KEY | 学号和外部 token 的 AES-GCM 密钥 |
| CONTROL_SERVICE_SIGNING_KEY | 付费 JWT/OAuth 签名私钥 |
| CONTROL_PUBLIC_BASE_URL | 公开基础 URL |
| CONTROL_ADMIN_BOOTSTRAP | 首次管理员初始化口令 |
| CONTROL_EVENT_RETENTION_DAYS | 原始事件保留天数 |
| CONTROL_RISK_LOGIN_WINDOW | 登录观察窗口 |
| CONTROL_RISK_LOGIN_COUNT | 登录次数观察阈值 |
| CONTROL_RISK_DEVICE_COUNT | 设备码数量观察阈值 |

所有公网 API 必须使用 HTTPS。服务端密钥不进入仓库，不写日志。

## 17. 实施顺序

### 阶段 1：基础服务和统计

- Go 服务、配置、日志和 migration；
- SQLite WAL；
- 管理员登录；
- 设备注册；
- 活跃事件；
- Vue 活跃总览。

### 阶段 2：用户和 control 会话

- challenge；
- 学号断言和固定 student_alias；
- 多设备关系；
- access/refresh token；
- 用户和设备后台。

### 阶段 3：付费文库

- 服务端签名密钥和 JWKS；
- 文库 audience JWT；
- device_serial claim；
- 文库首次绑定和换绑协议；
- APP 付费入口故障隔离。

### 阶段 4：OAuth

- OAuth client 管理；
- Authorization Code + PKCE；
- userinfo、revoke 和 scope；
- 其他系统接入。

### 阶段 5：风控列表

- 短时间登录多次记录；
- 账号设备码过多记录；
- 风控后台列表和已查看状态；
- 不实现任何自动处置。

## 18. 验收标准

- 同一学号可以在多台设备登录 control；
- 每台设备具有独立 device_serial 和设备密钥；
- 新设备登录不会使旧设备无法使用 control；
- 文库收到由 control 签名且不可由客户端修改的 device_serial；
- 文库可以按 device_serial 绑定和换绑；
- control 不可用时，OA 登录和所有免费功能不受影响；
- control 不可用时只影响新的付费鉴权、绑定和换绑；
- student_alias 始终符合固定算法且不带版本号；
- control 从不接收 OA 密码、Cookie 或 Ticket；
- 风控能列出短时间登录尝试多次和账号设备码过多；
- 风控不会自动封禁、拒绝、解绑或影响任何服务；
