[English](README.md) | 简体中文

# glwm2m

用 Go 实现的 LwM2M 服务端引擎。

> 状态：**开发快照（未打标签）** — 注册生命周期（过期、endpoint 冲突策略、peer 绑定与事件）；
> 服务器下行 Read/Write/Execute/Observe（UDP 与 DTLS）；对象模型校验（多资源 TLV、原子写）；
> 本地对象树、Observe/Notify 与 CoAP DTLS。
> 在稳定版本发布前不会打标签；请使用 commit 伪版本（pseudo-version）。

## 已实现

- 经 [`gcoap`](https://github.com/maxid/gcoap) 提供 CoAP/UDP 传输（其内部使用
  [`gnety`](https://github.com/maxid/gnety) 与 `gnety/codec/coap`）
- 注册：`POST /rd?ep=...&lt=...&b=...` → `2.01 Created` + Location-Path
- 更新：`POST /rd/{id}` → `2.04 Changed`
- 注销：`DELETE /rd/{id}` → `2.02 Deleted`
- 内存中的注册表，含 endpoint/lifetime/binding/version/object links/peer、`RegisteredAt`、
  `LastSeen`、`ExpiresAt` 与 epoch
- 过期扫描（`ExpiryInterval`），每个过期注册只发一次事件；超过 `MaxLifetime` 的 lifetime 会被
  截断，非法值回 4.00
- endpoint 冲突默认替换旧注册（epoch 递增，旧注册以 `replaced` 原因上报 deregistered）；
  `RejectEndpointConflict` 打开时回 4.09
- update/delete 绑定到注册时的 peer，其他 peer 得到 4.04；peer 地址变化会刷新绑定并丢弃旧的
  下行缓存
- `EventSink` 接收 `registered`、`updated`、`deregistered`（原因 `client`/`replaced`）与
  `expired`，回调在锁外执行
- `MaxRegistrations` 限制注册数（超出回 5.03）
- Object 19（Binary App Data Container）：`BinaryAppDataObject()` 定义、服务器侧
  `CreateBinaryDataInstance`/`ReadBinaryData`/`WriteBinaryData`/`DeleteBinaryDataInstance`
  与分块二进制传输，本地 `/objects/19` 同样支持
- 单资源读/写（text/plain）：`GET/PUT /objects/{oid}/{iid}/{rid}`
- 以 LwM2M TLV 进行对象/实例读/写：`GET/PUT /objects/{oid}` 与 `/objects/{oid}/{iid}`
  （TLV 线格式本身位于
  [`gnety/codec/tlv`](https://github.com/maxid/gnety)；本模块的 `tlv`
  包是其上的兼容垫片）
- 执行：`POST /objects/{oid}/{iid}/{rid}` → `2.04 Changed`
- Observe/Notify：GET + Observe=0；资源/实例/对象写入会触发通知
- 经 gcoap/gnety 提供 DTLS 传输（服务端侧）
- **服务器下行管理**：`Server.Read`、`Write`、`Execute`、`Observe` 向已注册 peer 发送真实
  CoAP 请求，支持单次操作超时与每注册的客户端缓存；4.xx 应答映射为类型化
  `*OperationError`，不当作传输失败
- 下行传输：默认 UDP；`Config.DownlinkTLS` 支持 DTLS；`Config.DownlinkDialer` 可完全替换
  dialer（代理、PSK、测试）
- 对象模型：`ObjectDef`/`ResourceDef` 声明操作、多实例与值类型，`NewObjectRegistry` 负责校验。
  配置注册表后，未知对象/资源回 4.04，不可写/不可执行回 4.05，非法值回 4.00；多资源在 TLV 中
  保留实例 ID（`MultipleResource`/`ResourceInstance`），TLV 写入全部校验通过后才原子应用

## 服务器下行

```go
regs := srv.Registrations() // 每个存活注册的快照
instance, resource := uint16(0), uint16(5700)

value, err := srv.Read(ctx, glwm2m.Operation{
    RegistrationID: regs[0].ID,
    Path:           glwm2m.Path{ObjectID: 3303, InstanceID: &instance, ResourceID: &resource},
})
if err != nil {
    return err
}

err = srv.Write(ctx, glwm2m.Operation{
    RegistrationID: regs[0].ID,
    Path:           glwm2m.Path{ObjectID: 3303, InstanceID: &instance, ResourceID: &resource},
    Payload:        []byte("22.5"),
})

observation, initial, err := srv.Observe(ctx, op, func(value glwm2m.Value) {
    // 后续通知
})
defer observation.Cancel(context.Background())
```

下行使用客户端注册时的源地址，这是 LwM2M 的标准假设：客户端在自己的注册 socket 上处理请求。
DTLS 设置 `Config.DownlinkTLS`；其他传输或需要覆盖地址时使用 `Config.DownlinkDialer`。
`DownlinkMaxRequestSize` 限制单次下行请求体（含 Block1）。

## Object 19：Binary App Data Container

`BinaryAppDataObject()` 返回标准 Object 19 定义，可直接放入对象注册表：

| Resource | 类型 | 操作 |
| --- | --- | --- |
| 0 Data | Opaque，多实例 | R/W |
| 1 Data Priority | Integer | R/W |
| 2 Data Creation Time | Time | R |
| 3 Data Description | String | R/W |
| 4 Data Format | String | R/W |
| 5 App ID | Integer | R/W |

Data 资源可保存多个二进制值，每个用 resource instance ID 区分。服务器侧 helper 向已注册设备发送
真实协议请求：

```go
// 创建 /19 实例并携带 4 KiB 二进制数据；Block1 自动分块。
instanceID, err := srv.CreateBinaryDataInstance(ctx, registrationID, []glwm2m.BinaryData{
    {ID: 7, Value: payload},
})

values, err := srv.ReadBinaryData(ctx, registrationID, instanceID) // GET /19/{iid}/0
err = srv.WriteBinaryDataInstance(ctx, registrationID, instanceID, glwm2m.BinaryData{ID: 7, Value: next})
err = srv.WriteBinaryData(ctx, registrationID, instanceID, values) // 替换 Data 集合
err = srv.DeleteBinaryDataInstance(ctx, registrationID, instanceID)
```

超过 CoAP 块大小的 payload 自动走 Block1，大 TLV 读取由 Block2 组装；Data 以 TLV
`MultipleResource` 传输，保留 resource instance ID。本地 `/objects` 树同样支持该对象：
对 `/objects/19` 发送 Object Instance TLV 的 `PUT` 会创建实例并返回 `2.01 Created`。

## 配置

| 字段 | 默认 | 含义 |
| --- | --- | --- |
| `DefaultLifetime` | 24h | 客户端未带 `lt` 时的 lifetime |
| `MaxLifetime` | 7d | 允许的最大 lifetime |
| `ExpiryInterval` | 1s | 过期扫描间隔；负数关闭 |
| `MaxRegistrations` | 1024 | 注册上限（超出回 5.03） |
| `RejectEndpointConflict` | false | 重复 endpoint 回 4.09，而不是替换 |
| `DownlinkTimeout` | 10s | 单次下行默认超时 |
| `DownlinkMaxRequestSize` | gcoap 默认（1 MiB） | 单次下行请求体上限 |
| `DownlinkTLS` | — | 下行 DTLS 客户端配置 |
| `DownlinkDialer` | — | 自定义下行 dialer |
| `Events` | nil | 注册生命周期事件 |
| `Objects` | nil | 对象注册表；nil 保持宽松的本地对象树 |

## 计划中

- Bootstrap / Queue Mode / OSCORE
- 可插拔注册存储与 per-peer 下行配额
- 真机/模拟器互操作矩阵

## 示例

```go
srv, _ := glwm2m.New(glwm2m.Config{Addr: ":5683"})
_ = srv.Start(context.Background())
```

## 可运行示例

[`examples/registration-demo`](examples/registration-demo/main.go) 在回环地址的临时端口上启动服务端，
经 CoAP 注册一个模拟设备，并通过服务端 API 读回一个资源。glwm2m 不提供客户端类型，
因此设备侧使用构建服务端的同一个库 gcoap；注册请求就是设备真实发送的
`POST /rd?ep=...&lt=...&b=...&lwm2m=...`：

```sh
go run ./examples/registration-demo
go run ./examples/registration-demo -addr 127.0.0.1:5683 -endpoint demo-device
```

## 版本管理

见 [VERSIONING.md](VERSIONING.md)。

## 许可证

MIT。见 [LICENSE](LICENSE)。
