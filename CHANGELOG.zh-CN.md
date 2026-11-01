[English](CHANGELOG.md) | 简体中文

# 变更日志

> 开发快照不打 tag；以下条目按时间顺序排列。

## [Unreleased]

- Object 19（Binary App Data Container）：`BinaryAppDataObject()` 提供标准注册表定义（Data Opaque/多实例 R/W，以及 Data Priority、Data Creation Time、Data Description、Data Format、App ID）。服务器侧 helper `CreateBinaryDataInstance`、`ReadBinaryData`、`WriteBinaryData`、`WriteBinaryDataInstance`、`DeleteBinaryDataInstance` 向已注册设备发送真实协议请求；Data 以 TLV `MultipleResource` 传输以保留 resource instance ID，超过 CoAP 块大小的 payload 自动走 Block1。本地对象树支持 `PUT /objects/19` 的 Object Instance TLV 并返回 `2.01 Created`。`Config.DownlinkMaxRequestSize` 限制单次下行请求体。
- 注册生命周期：`ExpiryInterval` 定期扫描，每个过期注册只上报一次（`EventExpired`）；重复 endpoint 默认替换旧注册并递增 epoch，`RejectEndpointConflict` 打开时回 4.09；update/delete 绑定注册 peer（不匹配回 4.04）；`MaxRegistrations` 限制注册数（超出回 5.03）；非法 lifetime 回 4.00，超过 `MaxLifetime` 截断。`Registration` 新增 `ObjectLinks`、`Peer`、`RegisteredAt`、`LastSeen`、`ExpiresAt`、`Epoch`；`EventSink` 在锁外上报 registered/updated/deregistered/expired。
- 服务器下行管理：`Read`、`Write`、`Execute`、`Observe` 向已注册 peer 发送真实 CoAP 请求，支持单次操作超时与每注册客户端缓存。默认 UDP，DTLS 用 `Config.DownlinkTLS`，`Config.DownlinkDialer` 可完全替换 dialer。错误应答返回 `*OperationError`，不重试。注册被关闭、过期或替换时，其客户端与 observation 一并丢弃。
- 对象模型：`ObjectDef`/`ResourceDef` 声明操作、多实例与值类型，`NewObjectRegistry` 负责校验。配置注册表后，未知对象/资源回 4.04，不可写/不可执行回 4.05，非法值回 4.00；多资源在 TLV 中保留实例 ID，TLV 写入全部校验通过后才原子应用。
- **破坏性变更：** `Server.SetResource` 现在返回 error，便于注册表校验生效；忽略返回值的既有调用仍可编译。`SetResourceInstance` 与 `ResourceInstances` 暴露多资源实例。
- LwM2M TLV 线格式迁移到 `gnety/codec/tlv`，与其它协议 codec 共享同一份有界、分类错误的实现。`glwm2m/tlv` 现在是兼容 shim：类型别名 + 转调 `Marshal`/`Unmarshal`，不含线格式逻辑，现有 import 保持可编译；server engine 直接 import gnety 包。编码保持逐字节一致：codec 按 identifier 顺序输出每个 scope，与本库原有顺序一致。输入校验更严格（同一 scope 重复 identifier、嵌套超过 16 层、container 携带 value 都拒绝），因此歧义 `PUT` 返回 `4.00 Bad Request`，而不是静默部分写入。多资源实例现在按实例 ID 分别存储，canonical 顺序不会再把它们折叠成一个值。

- 基于 gcoap 的最小 LwM2M 注册状态机：register、update、deregister、内存 registry。
- 通过 `/objects/{oid}/{iid}/{rid}` 做单 resource 读写（text/plain）。
- LwM2M TLV codec 与 object/instance 读写；execute endpoint。
- Observe/Notify：resource/instance/object 变化通知已注册 observer。
- DTLS 传输配置与 registration-over-DTLS 测试。
