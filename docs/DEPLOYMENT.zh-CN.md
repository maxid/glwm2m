[English](DEPLOYMENT.md) | 简体中文

# 部署：要配什么，以及配之前要知道什么

> 适用于当前代码树（2026-11-04）。这份文档讲怎么运行这个引擎，不讲协议；内部结构见
> `docs/ARCHITECTURE.zh-CN.md`。

## 形态

一个 `glwm2m.Server` 拥有一个 CoAP 监听器（UDP 或 DTLS）。设备在它上面注册，服务端发起的
Read/Write/Execute/Observe 默认发回注册来源地址，除非 `Config.DownlinkDialer` 另有指定。

注册数据只存在**本进程内存里**：没有共享存储、没有集群。按设备集合起一个服务端，并预期重启会
丢掉所有注册（每台设备会按自己的节奏重新注册）。如果某个部署不能接受这一点，就要在这台引擎
之前终结注册，或者等「可插拔注册存储」那一项落地后由实现来持久化。

## 端口与传输

| 配置项 | 含义 |
|---|---|
| `Config.Addr` | 监听地址，例如 `0.0.0.0:5683`；LwM2M/CoAP 的端口是 5683/UDP |
| `Config.Network` | `"udp"`（默认）或 `"dtls"` |
| `Config.TLS` | `gnety.TLSConfig`：`CertFile`、`KeyFile`、`CAFile`、`RequireClientCert`、`InsecureSkipVerify` |
| `Config.DownlinkTLS` | 回拨设备时用的 DTLS 客户端配置（客户端证书、校验设备的 CA） |
| `Config.DownlinkDialer` | 完全替换回拨拨号器：代理、固定地址、测试替身 |

设备必须在其注册来源地址（或 `DownlinkDialer` 给出的地址）上可达，任何服务端发起的操作才
可能成功。

服务端发起的 Read、Write、Execute 走注册到达的那个会话，因此从服务端自己的 socket 发出 ——
真实客户端愿意答复的地址，`Observe` 也注册在同一个会话上，请求体超过一个 CoAP 块时用 Block1
上传。只有注册没有活会话（另一种传输、对端换了地址）时才回退到拨号客户端。注册向外发起、但无法被回拨的 NAT 后设备，注册、更新、注销都正常，下行会超时。

## Bootstrap

本引擎**没有 bootstrap 接口**：不提供 `/bs`，也无法告诉设备该去哪里注册。在接口落地之前，
服务端 URI 要带外预置（出厂配置、DHCP 选项、服务端旁边的预置 API）。这是面向设备规模最大的
功能缺口。

## DTLS 与 PSK

证书式 DTLS 是端到端支持的：监听侧用 `CertFile`/`KeyFile`，可选 `CAFile` 与
`RequireClientCert` 做双向认证，回拨侧用 `DownlinkTLS`。`InsecureSkipVerify` 只为开发存在，
不应进入生产。

**不支持预共享密钥（PSK）。** gnety 的传输没有 PSK 模式，所以只带 PSK 的设备群（NB-IoT 等
LPWAN 模组常见）目前无法在这里终结 DTLS。现实的两条路：在引擎前面放一个 DTLS 终结器，把明文
CoAP 转发到回环；或者先扩展传输层。

## 注册容量

| 配置项 | 默认 | 作用 |
|---|---|---|
| `MaxRegistrations` | 1024 | 硬上限，超出注册回 `5.03` |
| `DefaultLifetime` | 24h | 注册未带 `lt` 时使用 |
| `MaxLifetime` | 7 天 | 客户端请求更大的 lifetime 会被截断到这里 |
| `ExpiryInterval` | 1s | 过期扫描间隔；为负则关闭扫描 |

lifetime 短：重启后恢复快，但更新流量多；lifetime 长：更新少，但崩溃后「已死设备仍算在线」的
窗口更长。`MaxRegistrations` 同时也是内存上界：一条注册持有它的 endpoint、对象清单与对端身份。

## 容量

下面的 CoAP 层有自己的限额 —— 观察者、块大小、去重缓存、交换时序 —— 它们都是 gcoap 的默认
值。`glwm2m.Config` 还没有把它们暴露出来，所以需要超过 gcoap 默认观察者预算的部署，要么等这
层打通，要么在自行嵌入引擎时直接配置 gcoap。

`DownlinkTimeout`（10s）限制单次服务端发起操作；慢设备最多阻塞调用方这么久。

## 可观测性

`Config.Events` 是可观测面：`registered`、`updated`、`deregistered`（reason 为 `client` 或
`replaced`）与 `expired`（reason `lifetime`）。sink 在锁之外的库 goroutine 上运行，不能阻塞；
慢 sink 会拖慢请求或过期扫描。

引擎自身没有指标钩子，gcoap 的 `Metrics` 也没有通过 `glwm2m.Config` 暴露。需要计数的部署只能
从事件 sink 推导（并接受它是注册级、不是包级）。

## 升级

快照不打 tag，按提交伪版本发布；在 `go.mod` 里显式钉住 `gcoap` 与 `gnety`，与本文模块一起
升级。API 稳定性是 `ROADMAP.md` 的一项（`v1.0`），在那之前把导出类型当作会动的。重启会丢注册
—— 把升级安排在「设备集中重新注册」可以接受的时候。

## 安全说明

- 更新与注销绑定在注册该 endpoint 的对端上；别的对端回 `4.04`。在明文 UDP 上「对端」就是一个
  地址，路径外攻击者可以伪造 —— 让这个绑定真正有意义的是 DTLS。
- 同一 endpoint 的第二次注册默认**替换**第一次。当设备群不应通过重注册顶掉同一 endpoint 的
  另一台设备时，置 `RejectEndpointConflict`。
- `MaxRegistrations` 限制注册洪泛能达到的内存；替换会关闭被替换注册缓存的下行。
