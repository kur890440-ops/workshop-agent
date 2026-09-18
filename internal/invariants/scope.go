package invariants

// ScopeContext is built from authenticated domain records, not model output.
// Module and process IDs are application-owned identifiers.
type ScopeContext struct {
	WorkshopID, ModuleID, ProcessID, ProductID, RoleID int64
}

func Applies(r Rule, c ScopeContext) bool {
	if r.ScopeType == "SYSTEM" {
		return r.ScopeID == nil
	}
	if r.ScopeID == nil {
		return false
	}
	ids := map[string]int64{"WORKSHOP": c.WorkshopID, "MODULE": c.ModuleID, "PROCESS": c.ProcessID, "PRODUCT": c.ProductID, "ROLE": c.RoleID}
	id, ok := ids[r.ScopeType]
	return ok && id > 0 && id == *r.ScopeID
}

func (r InvariantRegistry) ForTask(phase string) []Rule {
	action := "start_production"
	if phase == "execution" || phase == "validation" {
		action = "complete_task"
	}
	return r.ForAction(ProposedAction{ActionType: action})
}
