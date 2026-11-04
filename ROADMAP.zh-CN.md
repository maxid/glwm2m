[English](ROADMAP.md) | 简体中文

# 路线图

- [x] CoAP 传输集成（gcoap/gnety）
- [x] 注册/更新/注销状态机
- [x] Observe/Notify
- [x] 读/写（text/plain 单 resource）
- [x] TLV 多 resource 读写与 Execute
- [x] DTLS 传输
- [x] 注册生命周期：过期扫描、endpoint 冲突策略、epoch、peer 绑定的 update/delete、生命周期事件、注册上限
- [x] 服务器下行 Read/Write/Execute/Observe（UDP 与 DTLS）
- [x] 对象注册表（操作/多实例/类型）、按实例存储的多资源与原子 TLV 写入
- [x] Object 19 Binary App Data Container：标准定义、分块下行 create/read/write/delete 与本地对象树支持
- [x] 模拟设备端到端测试（注册、下行、Observe）
- [x] 与独立实现的互操作：Eclipse Leshan 客户端 demo 注册后，解析结果与库自带客户端一致
      （`interop/`）
- [ ] 下行走监听器自己的 socket：真实客户端只接受注册时那个服务端地址的请求，所以服务端发起的
      Read/Write/Execute/Observe 目前到不了它（`interop/` 记录了这一缺口）
- [ ] 与真机/厂商模拟器的互操作测试（双向）
- [ ] 可插拔注册存储与 per-peer 下行配额
- [ ] v1.0 API 稳定化
