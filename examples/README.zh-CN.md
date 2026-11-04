[English](README.md) | 简体中文

# 示例

两个可直接运行的程序，每个回答一个问题。两者都是自包含的 `main` 包，在仓库根目录
`go run ./examples/<name>` 即可，不需要 LwM2M 设备、不需要 Leshan 实例、也不需要配置文件。
它们各自在回环端口上起服务端，需要时连模拟设备一起起。

- [registration-demo](#registration-demo) —— 注册生命周期长什么样、服务端从注册里解析出什么、
  本地对象树如何回答一次写入。
- [observe-telemetry](#observe-telemetry) —— LwM2M 完整的一圈：服务端对已注册设备发起 Read 与
  Observe，随后设备的一次上报在没有任何请求的情况下到达服务端。

## 统一约定

- 每个步骤一行 —— `[n/N] 动作 … 结果` —— 结束时一行 `[done] …`。
- 出错打到 stderr，格式 `<示例名>: <动作>: <错误>`，退出码非 0。
- Ctrl-C 都能停：脚本式的一轮打印 `[stop] interrupted`，长期运行的 `-serve` 打印
  `[stop] server stopped`，退出码都是 0 —— 示例不是服务。
- `-h` 打印这个示例回答什么问题，外加可以直接复制的命令。
- 脚本式的运行监听回环地址的临时端口并打印实际地址；`-serve` 用 CoAP 的
  `127.0.0.1:5683`，端口被占用时挪到下一个空闲 UDP 端口并打印出来，所以它打印的地址
  永远是可以直接用的那个。

## registration-demo

**回答：** 设备为了被注册要发哪些请求、服务端从中保留了什么，以及设备经 CoAP 写进去的值
为什么服务端不用再发请求就能拿到。

```sh
go run ./examples/registration-demo
go run ./examples/registration-demo -endpoint sensor-01 -timeout 2s
go run ./examples/registration-demo -serve   # 然后：coap-client -m post "coap://127.0.0.1:5683/rd?ep=dev&lt=60&b=U&lwm2m=1.0"
```

应该看到：

```
[1/6] server listening on 127.0.0.1:51175
[2/6] device "demo-device" registered as a826c9247143 (2.01)
[3/6] server parsed it: endpoint=demo-device lifetime=60s binding=U version=1.0 links=</3303/0> (live: 1)
[4/6] device PUT /objects/3303/0/5700 -> 2.04
[5/6] device GET /objects/3303/0/5700 -> 2.05 "21.5"
[6/6] server.Resource(3303, 0, 5700) = "21.5" (no request needed)
[done] registration lifecycle demo complete; server and device closed
```

注册 id 由服务端生成，放在 `2.01` 的 `Location-Path` 选项里回给设备。`-serve` 改为把服务端
挂着，外部 LwM2M 客户端注册、更新、离开时，通过 `Config.Events` 把每个生命周期事件
（`registered`、`updated`、`deregistered`、`expired`）打印出来。

## observe-telemetry

**回答：** LwM2M 比 CoAP 请求/响应多出来的东西 —— 服务端对设备发起 Read 与 Observe，以及
设备的一次上报以通知形式到达，服务端没有再问一次。

```sh
go run ./examples/observe-telemetry
go run ./examples/observe-telemetry -endpoint sensor-01
go run ./examples/observe-telemetry -serve   # 然后：coap-client -m post "coap://127.0.0.1:5683/rd?ep=dev&lt=60&b=U&lwm2m=1.0"
```

应该看到：

```
[1/7] device serving GET /3303/0/5700 on 127.0.0.1:52429
[2/7] LwM2M server listening on 127.0.0.1:62577, downlink dials the device
[3/7] device "telemetry-device" registered as a92f3464eec5 (2.01 Created)
[4/7] server Read /3303/0/5700 -> "21.5"
[5/7] server Observe /3303/0/5700 -> initial "21.5"
[6/7] device reported "22.0" -> server received "22.0" (no request)
[7/7] device deregistered a92f3464eec5 (2.02)
[done] registration, read, observe and deregistration complete
```

输出里看不到的两个实现细节：

- 真实的 LwM2M 客户端**两个角色用同一个 socket**：注册和对外提供对象树在同一个地址上。
  gcoap 的客户端与服务端各自持有独立的 socket，所以示例用 `Config.DownlinkDialer` 把下行
  指向设备监听地址 —— 这正是库文档里为「注册来源地址描述不了的地址」准备的钩子。
  `-serve` 不需要它：那里服务端回拨的是真实客户端注册时用的地址。
- 下行使用 LwM2M 客户端形式 `/{oid}/{iid}/{rid}` —— 真机服务的就是这个路径。服务端自己的
  `/objects/...` 端点（`registration-demo` 用的那个）是另一层接口，不会出现在发给设备的线路上。

注册与观察的状态机见 [`docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.zh-CN.md)；bootstrap、
DTLS/PSK 取舍与注册存储的选型见 [`docs/DEPLOYMENT.md`](../docs/DEPLOYMENT.zh-CN.md)。
