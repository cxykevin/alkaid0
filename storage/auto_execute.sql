-- 数据库维护语句：只在每个数据库文件首次初始化时执行一次（storage/storage.go 的
-- runMaintenanceOnce 用进程内表去重）。
--
-- 注意：per-connection 的 PRAGMA（foreign_keys / journal_mode / synchronous /
-- cache_size / mmap_size / page_size）**不在这里**，而是由 storage/init.go 的
-- perConnPragmas 通过 DSN 的 _pragma 参数下发。原因：用 db.Exec 执行 PRAGMA 只对
-- 当时那一条连接有效，连接池回收（ConnMaxLifetime 到期 / 连接出错被丢弃）后新建的
-- 连接会退回 SQLite 默认值，foreign_keys 会静默变成 OFF。
--
-- 同理这里也不能放 PRAGMA：runMaintenanceOnce 每个文件只跑一次，放进来的话
-- 连接回收后同样失效。VACUUM/ANALYZE 则相反——它们会扫描/重写整个数据库，
-- 放进去重执行一次的路径才不会在每次打开数据库时都做一遍。

VACUUM;

ANALYZE;
