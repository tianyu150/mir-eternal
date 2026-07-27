# Go 迁移任务记录

每个完成的迁移任务必须在本目录新增一份记录，说明迁移边界、兼容协议、数据来源、测试和已知限制。记录按完成顺序编号，并与阶段性 Git 提交一起提交。

| 编号 | 任务 | 状态 |
| --- | --- | --- |
| 001 | AccountServer、票据与 GameServer 登录核心 | 已完成，见 `docs/go-architecture.md` |
| 002 | 地形、世界 Actor、AOI 与玩家移动 | 已完成，见 `docs/go-architecture.md` |
| 003 | 传送门与地图切换 | 已完成，见 [003-teleport-gates.md](003-teleport-gates.md) |
| 004 | 静态守卫/NPC 与 AOI | 已完成，见 [004-static-guards.md](004-static-guards.md) |
