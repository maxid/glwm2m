[English](CHANGELOG.md) | 简体中文

# 变更日志

> 开发快照不打 tag；以下条目按时间顺序排列。

## [Unreleased]

- `Config.MaxDownlinksPerPeer`（8）限制单个对端地址能持有的下行 —— 在途的
  Read/Write/Execute 加上它上面注册的每个观察。超过上限的新操作以 `ErrTooManyDownlinks`
  拒绝，该对端已有的操作继续工作，其它对端不受影响；同一地址重连仍算同一份预算。

- `benchmark_test.go` 与 `docs/PERFORMANCE.md`（含中文版）：回环 UDP 上一次注册、一条
  observe 通知的实测数字，附机器、边界与复现命令。基准不进 `go test ./...`、不进 CI，数字是
  基线而非目标。
- **修复：** `2.01 Created` 现在把注册 id 放在 `Location-Path` 里（LwM2M 1.0 的要求）；此前
  用的是 `URI-Path`，会把位置存下来用于注销的客户端因此读错选项。Object 19 的创建辅助函数
  对设备返回的两种选项都接受。

- 超过一个 CoAP 块的下行请求体现在通过注册所在会话用 Block1 上传，真实客户端也会接受大
  写入；只有注册没有活会话时才使用拨号客户端。

- `Observe` 现在与 Read/Write/Execute 一样，注册在注册到达的那个会话上
  （`gcoap.Server.ObserveTo`）：真实 LwM2M 客户端会接受观察，通知也能到达。注册没有活会话时
  仍回退到拨号客户端。`interop/` 对 Leshan 断言了整条路径（注册、Read、Observe）。

- 服务端发起的 Read、Write、Execute 现在走注册到达的那个会话（`gcoap.Server.DoTo`），
  请求因此从服务端自己的 socket 发出 —— 合规 LwM2M 客户端只答复这个来源。注册没有活会话、
  或请求体需要 Block1 时回退到拨号客户端。`Observe` 仍用拨号客户端：`interop/` 记录了 Leshan
  的拒绝，`ROADMAP.md` 跟踪把它也搬到会话上。

- `interop/`：对独立 LwM2M 实现的互操作模块 —— Eclipse Leshan 客户端 demo（Java/Californium）
  注册到本服务端，解析结果（含对象清单与 lifetime）与库自带客户端一致。该模块也如实记录一个真实
  限制：客户端只答复它注册时用的服务端地址，而下行从自己的 socket 发出，因此 Leshan 会以
  `5.00 "unknown server"` 拒绝服务端发起的操作。
- CI 增加 `integration` 作业：拉取 Leshan 客户端 jar 并跑
  `go test -tags=integration ./...`；缺 JRE 或 jar 时测试自动跳过。

- **Breaking：** 服务端发起的操作现在用 LwM2M 客户端路径
  `/{object}[/{instance}[/{resource}]]` 访问设备。此前发的是本服务端自己的 `/objects/...`
  形式，真实 LwM2M 客户端会回 4.04 —— 也就是 `Read`、`Write`、`Execute`、`Observe` 与
  Object 19 辅助函数只能打到达本库本地端点形态的设备。`Path.String()` 现在渲染客户端形式，
  `CreateBinaryDataInstance`/`DeleteBinaryDataInstance` 的请求也随之改变。走导出 API 的程序
  不需要改；只有设备侧曾按 `/objects/...` 提供对象的 handler 需要换成 LwM2M 路径。本服务端
  自身的 `/objects/...` 端点不变。

- 两个可运行示例与双语的 `examples/README`：`registration-demo`（注册生命周期、服务端解析出的
  注册、本地对象树对一次写入的应答，以及外部 LwM2M 客户端可注册的 `-serve` 模式）与
  `observe-telemetry`（服务端发起的 Read 与 Observe，随后设备上报以通知形式到达）。两者都打印
  `[n/N]` 步骤与 `[done]`，Ctrl-C 停止且退出码 0，`-h` 给出可直接复制的命令。
- `docs/ARCHITECTURE.md` 与 `docs/DEPLOYMENT.md`（各有中文版）：注册与观察状态机、对象树及其
  TLV 端点、下行与其客户端缓存、限额表，以及设备群逼出来的部署决定 —— bootstrap、
  只支持证书 DTLS 的取舍、内存注册存储与重启行为。
- `fuzz_test.go`：`FuzzDecodeTLV` 走 TLV 编解码与「条目变成对象值」的写入路径（不 panic、
  重编码规范、值不超过输入规模、存储增长速度不超过请求体）；`FuzzDecodeRegistration` 走注册的
  每个字段并钉住其契约（都有确定答复、endpoint 原样保留、lifetime 有上限、拒绝不留状态）。
- CI 补上 `race` 与两个 fuzz 目标的短时运行；`test` 矩阵保持原样。

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
