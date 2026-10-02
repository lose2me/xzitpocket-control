<div align="center">

# XzitPocket Control - 掌上徐工控制台

[![License: GPL](https://img.shields.io/badge/License-GPLv3-yellow.svg)](https://opensource.org/licenses/gpl-3-0)

</div>

[掌上徐工控制台](https://github.com/lose2me/xzitpocket-control) 是一个开源、模块化的 Go + SQLite 单体服务，内置 Vue 3 管理后台，为 [掌上徐工](https://github.com/lose2me/xzitpocket) 提供设备登记、用户会话、活跃统计、风控记录和内置文库。

> OA 登录始终由 xzitpocket 客户端完成。control 只接收客户端提交的学号、固定伪名和设备断言，不读取、不转发、不保存 OA 凭据。

<div align="center">

***``不接触 OA 凭据`` ``永久开源``***

</div>

**功能 | 已实现**:
- [x] 设备登记与设备签名校验
- [x] 用户会话、刷新与撤销
- [x] 同一学号多设备登录
- [x] 活跃统计与 DAU / WAU / MAU 趋势
- [x] 风控记录
- [x] 错误上报归档与忽略
- [x] 内置题库与 CDK 解锁
- [x] APP 发布与校历配置
- [x] 内置 Vue 3 管理后台
*以优先级排序*

## 运行

Go
```
go run ./cmd/server
```

默认地址为 `http://127.0.0.1:8080`，管理后台和 API 同源。配置读取运行目录的 `data/.env`，模板见 [`data/.env.example`](data/.env.example)；`data/.env` 和数据库不会提交到仓库。

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

前端资源通过 `go:embed` 打进二进制，无独立构建步骤。

完整设计见 [`fmd/system-design.md`](fmd/system-design.md)。
