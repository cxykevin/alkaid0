package request

import _ "embed" // embed expr

//go:embed rules/approve.expr
var builtinAutoApproveExpr string

//go:embed rules/reject.expr
var builtinAutoRejectExpr string
