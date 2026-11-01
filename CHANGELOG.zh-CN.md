[English](CHANGELOG.md) | 简体中文

# 变更日志

> 开发快照不打 tag；以下条目按时间顺序排列。

## [Unreleased]

- LwM2M TLV 线格式迁移到 `gnety/codec/tlv`，与其它协议 codec 共享同一份有界、分类错误的实现。`glwm2m/tlv` 现在是兼容 shim：类型别名 + 转调 `Marshal`/`Unmarshal`，不含线格式逻辑，现有 import 保持可编译；server engine 直接 import gnety 包。编码保持逐字节一致：codec 按 identifier 顺序输出每个 scope，与本库原有顺序一致。输入校验更严格（同一 scope 重复 identifier、嵌套超过 16 层、container 携带 value 都拒绝），因此歧义 `PUT` 返回 `4.00 Bad Request`，而不是静默部分写入。canonical 顺序的一个细节：`applyTLV` 每个 resource identifier 只存一个值，所以多实例 resource 中 instance id 最大的子项胜出，而此前由线顺序决定。按实例存储是路线图中的 object-model 缺口。

  **构建提示**：在 gnety commit 推送前，`go.mod` 需要 `replace github.com/maxid/gnety => ../gnety`；推送后移除并 pin pseudo-version。

- 基于 gcoap 的最小 LwM2M 注册状态机：register、update、deregister、内存 registry。
- 通过 `/objects/{oid}/{iid}/{rid}` 做单 resource 读写（text/plain）。
- LwM2M TLV codec 与 object/instance 读写；execute endpoint。
- Observe/Notify：resource/instance/object 变化通知已注册 observer。
- DTLS 传输配置与 registration-over-DTLS 测试。
