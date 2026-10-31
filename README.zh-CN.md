[English](README.md) | 简体中文

# glwm2m

用 Go 实现的 LwM2M 服务端引擎。

> 状态：**开发快照（未打标签）** — 注册/更新/注销、
> 本地对象读/写/执行、Observe/Notify 以及 CoAP/UDP 上的 DTLS。
> 服务端发起的设备管理、注册过期与完整的对象模型
> 校验尚未完成。
> 在稳定版本发布前不会打标签；请使用 commit 伪版本（pseudo-version）。

## 已实现

- 经 [`gcoap`](https://github.com/maxid/gcoap) 提供 CoAP/UDP 传输（其内部使用
  [`gnety`](https://github.com/maxid/gnety) 与 `gnety/codec/coap`）
- 注册：`POST /rd?ep=...&lt=...&b=...` → `2.01 Created` + Location-Path
- 更新：`POST /rd/{id}` → `2.04 Changed`
- 注销：`DELETE /rd/{id}` → `2.02 Deleted`
- 内存中的注册表，含 endpoint/lifetime/binding/version
- 单资源读/写（text/plain）：`GET/PUT /objects/{oid}/{iid}/{rid}`
- 以 LwM2M TLV 进行对象/实例读/写：`GET/PUT /objects/{oid}` 与 `/objects/{oid}/{iid}`
  （TLV 线格式本身位于
  [`gnety/codec/tlv`](https://github.com/maxid/gnety)；本模块的 `tlv`
  包是其上的兼容垫片）
- 执行：`POST /objects/{oid}/{iid}/{rid}` → `2.04 Changed`
- Observe/Notify：GET + Observe=0；资源/实例/对象写入会触发通知
- 经 gcoap/gnety 提供 DTLS 传输（服务端侧）

## 计划中

- Object 19 与二进制代码流支持
- Bootstrap / Queue Mode / OSCORE

## 示例

```go
srv, _ := glwm2m.New(glwm2m.Config{Addr: ":5683"})
_ = srv.Start(context.Background())
```

## 版本管理

见 [VERSIONING.md](VERSIONING.md)。

## 许可证

MIT。见 [LICENSE](LICENSE)。
