package structs

// Tables 所有表。
//
// 注意：Metadata（metadata 表）不在此列表中——它的存在本身就是「树形消息迁移
// 已完成」的标记（见 storage/migrate.MigrateToTree），由迁移函数在事务内创建。
// 若加入这里，InitDB 的 AutoMigrate 会在迁移检查之前把表建出来，导致历史库的
// 线性消息永远不会被回填。
var Tables = []any{
	&Chats{},
	&Messages{},
	&SubAgents{},
	&Terminals{},
	&Scopes{},
	&Configs{},
	&Traces{},
	&ReferFiles{},
	&ClassifySegment{},
	&KeyMapping{},
	&CustomMask{},
	&Workflows{},
	&WorkflowEvents{},
}
