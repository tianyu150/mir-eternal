# Go 服务运行手册

## 环境

- Go 1.25 或更高版本；
- 与当前仓库兼容的 Legend Eternal 客户端；
- `Database/System/GameMap` 下的地图、区域和 terrain 数据；
- UDP 7000、UDP 6678 和 TCP 8701 可用。

## 本地运行

默认配置让两个服务运行在同一台主机：

```bash
export MIR_TICKET_SECRET='replace-with-a-long-random-secret'
go run ./cmd/gameserver -config configs/gameserver.json
go run ./cmd/accountserver -config configs/accountserver.json
```

也可以构建独立程序：

```bash
make build
./bin/gameserver -config configs/gameserver.json
./bin/accountserver -config configs/accountserver.json
```

数据默认写入：

```text
data/accounts/       与旧 C# AccountServer 文件兼容
data/game/game.json  Go GameServer 角色及离线世界状态数据库
```

迁移旧账号时，停止旧 AccountServer，将原 `Accounts` 目录复制到 `data/accounts`。建议先备份；Go 服务会在旧明文账号登录后更新对应文件。

## Docker Compose

```bash
cd deploy
export MIR_TICKET_SECRET="$(openssl rand -hex 32)"
docker compose up --build
```

Compose 发布：

- `7000/udp`：Launcher；
- `8701/tcp`：游戏客户端。

`6678/udp` 只在 Compose 内部网络使用，不应发布到公网。

## 配置要点

AccountServer 的服务器配置区分：

- `public_address`：返回给 Launcher 的游戏 TCP 地址；
- `ticket_address`：AccountServer 访问的 GameServer 内部 UDP 地址。

两者在 NAT 或容器环境中通常不同。

GameServer 的 `ticket_allowed_cidrs` 为空时不限制 UDP 来源；如果未配置 HMAC 密钥，应至少将其限制为 AccountServer 的内部网段。生产环境建议同时使用 CIDR 白名单和 `MIR_TICKET_SECRET`。

## 验证

```bash
make fmt-check
make generate-check
make vet
make test-race
make build

curl http://127.0.0.1:9100/healthz
curl http://127.0.0.1:9101/stats
```

## 回滚

Go 和 C# AccountServer 使用同类账号文件，但切换前仍必须备份。GameServer 的 Go 数据库不是旧 C# 用户数据库的直接替代品。若需要回滚世界逻辑，停止 Go GameServer 并重新启动旧 C# GameServer；不要将 `data/game/game.json` 覆盖到旧数据库目录。
