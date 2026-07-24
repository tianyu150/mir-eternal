# Go 服务端架构与迁移状态

本目录记录 `AccountServer` 和 `GameServer` 从 C# 向 Go 的渐进式迁移。旧 C# 项目暂时保留，作为协议和尚未迁移业务的权威参考；Go 服务不依赖 Windows Forms，可运行于 Linux、Windows 和容器环境。

## 目录结构

```text
cmd/accountserver/       AccountServer 进程入口
cmd/gameserver/          GameServer 进程入口
cmd/protocolgen/         从 C# PacketInfo 属性生成协议目录
internal/account/        账号领域、BCrypt 和旧 JSON 仓储
internal/accountserver/  启动器 UDP 协议及服务器
internal/ticket/         一次性登录票据和 HMAC 内部协议
internal/gameprotocol/   TCP 拆包、组包、XOR 和 525 个协议描述
internal/gamestore/      游戏账号及角色选择数据持久化
internal/gameserver/     票据接收、会话状态机和角色选择处理
internal/observability/  健康检查和运行统计
configs/                 本地配置
deploy/                  Docker 和 Compose 配置
```

## 服务拓扑

```text
Launcher -- UDP/7000 --> AccountServer
                              |
                   signed UDP ticket/6678
                              |
                              v
Client   -- TCP/8701 --> GameServer --> character database
```

1. Launcher 使用原空格分隔协议完成注册或账号验证。
2. 启动游戏时，AccountServer 生成客户端可识别的 `ULS21-` 票据。
3. AccountServer 通过内部 UDP 将票据发送给 GameServer。
4. 客户端在 TCP 登录包 `1001` 中提交票据。
5. GameServer 原子消费票据。同一票据不能再次使用。

生产环境必须为两个服务设置相同的 `MIR_TICKET_SECRET`。启用后，内部票据使用 HMAC-SHA256 签名并包含过期时间；客户端看到的票据格式不变。

## 已迁移范围

### AccountServer

- 登录、注册、重置密码和开始游戏四类 Launcher 请求。
- 与原返回代码和服务器列表格式兼容。
- BCrypt 密码；旧明文账号在首次成功登录时原地升级。
- 直接读取旧 `Accounts/*.txt` 文件。
- 大小写不敏感索引、原子文件替换、并发保护。
- 有界请求并发、数据报长度限制和优雅关闭。

### GameServer 核心

- TCP 游戏监听器和 UDP 票据监听器。
- 登录/空闲超时、最大连接数、重复账号检测。
- 一次性票据、过期清理、容量限制和来源 CIDR 限制。
- 登录、角色列表、创建、软删除、恢复、永久删除、进入游戏及 Ping。
- 原 C# 登录协议块、角色列表模板和 94 字节角色描述布局。
- 版本化、原子提交的账号/角色持久化。

### 协议

`catalog_gen.go` 从 C# 源文件生成，目前包含：

- 客户端协议：218 个。
- 服务端协议：307 个。
- 合计：525 个。

协议层支持固定长度、16 位动态长度、32 位动态长度、粘包/半包和从第 4 字节开始的 `0x81` XOR 转换。修改 C# 协议属性后执行：

```bash
go generate ./internal/gameprotocol
```

## 尚未迁移

下面的 C# 世界逻辑尚未宣称完成：

- 地图实例、AOI 和地图主循环；
- 玩家、怪物、宠物、守卫和陷阱对象；
- 移动、战斗、技能、Buff 和掉落；
- 背包、装备、商店和仓库；
- NPC、任务、组队、好友、师徒和邮件；
- 公会、攻城、竞技场和 GM 命令；
- 旧 GameServer 用户数据库的完整导入。

这些数据包已经可以安全拆包，但在业务处理器迁移前会增加 `unhandled_packets`，不会伪造成功响应。后续业务应按领域注册到 `session.handle` 的分发边界，并为每个状态转换增加协议夹具测试。

## 数据一致性

当前持久化实现使用版本化 JSON 快照，写入流程为：

1. 在锁内克隆当前快照；
2. 对副本执行领域校验和修改；
3. 写入同目录临时文件；
4. `fsync` 后原子 `rename`；
5. 仅提交成功后替换内存状态。

这避免部分写入及内存成功、磁盘失败的不一致。`Repository`/`Store` 边界允许后续增加 PostgreSQL 或其他实现，而不改变网络层。

## 可观测性

两个进程都提供仅使用标准库的管理端点：

```text
GET /healthz
GET /stats
```

默认地址：

- AccountServer：`127.0.0.1:9100`
- GameServer：`127.0.0.1:9101`

GameServer 的 `unhandled_packets` 是迁移进度和客户端兼容性的重要指标。
