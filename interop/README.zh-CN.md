[English](README.md) | 简体中文

# interop

与独立 LwM2M 实现的互操作测试：[Eclipse Leshan](https://github.com/eclipse-leshan/leshan)
客户端 demo —— 一个跑在另一套 CoAP 栈（Californium）上的真实 LwM2M 客户端。

测试放在独立 Go module 里，让库的模块图不必背上 Java 运行时或第二套 CoAP 栈。该模块把
`github.com/maxid/glwm2m` replace 到父目录，因此永远测的是工作副本，只用于检出后运行，不单独
发布。

## 怎么跑

测试带 `integration` tag；没有 JRE 与 Leshan demo jar 时会自动跳过：

```sh
# 在本目录执行一次（jar 在 .gitignore 里）：
curl -sSO https://repo1.maven.org/maven2/org/eclipse/leshan/leshan-client-demo/2.0.0-M15/leshan-client-demo-2.0.0-M15-jar-with-dependencies.jar
mv leshan-client-demo-2.0.0-M15-jar-with-dependencies.jar leshan-client-demo.jar

go test -tags=integration ./...
```

`LESHAN_CLIENT_JAR` 可以指向别处的 jar。CI 用 JDK 17 跑，GitHub 的 ubuntu 镜像自带。

## 证明了什么

- **注册。** Leshan 的 demo 客户端（LwM2M 1.1/1.2、Californium）注册到本服务端：请求被解析成
  与库自带客户端完全相同的 `Registration`，endpoint、lifetime、version、peer 与对象清单都
  完好。
- **Read 走服务端自己的 socket。** `Server.Read` 把请求发在注册到达的那个会话上
  （`gcoap.Server.DoTo`），Leshan 因此接受并答出 Device 对象的厂商 —— 来自其它来源的请求会被
  当作 "unknown server" 拒绝。
- **一个仍然存在的缺口，故意的。** `Observe` 仍会另拨客户端，Leshan 以 `5.00 "unknown server"`
  拒绝；测试把这个事实记录下来而不是掩盖它，缺口修好时该测试会失败，提示同时更新
  `ROADMAP.md`、`docs/ARCHITECTURE.md` 与测试本身。

## 不覆盖什么

- 反方向（Leshan 的 *server* demo，本引擎当设备）没有测：`glwm2m` 是服务端引擎，而设备需要用
  同一个 socket 既注册又对外提供对象树，gcoap 的客户端/服务端拆分做不到。单元测试里的假设备在
  本地覆盖了那个形状。
- Leshan 与本引擎之间的 DTLS：测试跑明文 CoAP。
