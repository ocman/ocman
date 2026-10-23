package factorymod

import (
	"net/http"

	"github.com/NoUseFreak/ocman/internal/module"
)

// Routes mounts Factory's own mux under /api/factory/. Keeping the routes
// off the server mux lets a known path with the wrong method answer 405
// rather than reaching the server's catch-all 404.
func (m *Module) Routes(r module.Router) {
	mux := http.NewServeMux()
	local := m.deps.Guards.Localhost

	mux.HandleFunc("GET /api/factory/status", m.handleFactoryStatus)
	mux.HandleFunc("GET /api/factory/queue", m.handleFactoryQueue)
	mux.HandleFunc("POST /api/factory/recovery-gates/{gate}/{action}", m.handleFactoryRecoveryGate)
	mux.HandleFunc("POST /api/factory/authority-gates/{gate}/{action}", m.handleFactoryAuthorityGate)
	mux.HandleFunc("POST /api/factory/project-gates/{gate}/{action}", m.handleFactoryProjectGate)
	mux.HandleFunc("GET /api/factory/configuration", m.handleGetConfiguration)
	mux.HandleFunc("POST /api/factory/configuration", local(m.handleSetConfiguration))

	for _, formulas := range []string{"/api/factory/formulas", "/api/factory/formulas/{$}"} {
		mux.HandleFunc("GET "+formulas, m.handleListFormulas)
		mux.HandleFunc("POST "+formulas, local(m.handleSaveFormula))
	}
	mux.HandleFunc("POST /api/factory/formulas/validate", local(checkFormula(m.svc.ValidateFormula)))
	mux.HandleFunc("POST /api/factory/formulas/preview", local(checkFormula(m.svc.PreviewFormula)))
	mux.HandleFunc("GET /api/factory/formulas/{formula}/{version}", m.handleGetFormula)

	const epic = "/api/factory/epics/{epic}"
	const issue = epic + "/issues/{issue}"
	mux.HandleFunc("GET /api/factory/epics", m.handleListEpics)
	mux.HandleFunc("POST /api/factory/epics", local(m.handleFactoryEpicCreate))
	mux.HandleFunc("GET "+epic, m.handleGetEpic)
	mux.HandleFunc("GET "+epic+"/usage", m.handleFactoryUsage)
	mux.HandleFunc("DELETE "+epic+"/projects", local(m.handleRemoveEpicProject))
	mux.HandleFunc("GET "+epic+"/issues", m.handleListIssues)
	mux.HandleFunc("GET "+epic+"/removed-issues", m.handleListRemovedIssues)
	mux.HandleFunc("GET "+issue+"/comments", m.handleListIssueComments)
	mux.HandleFunc("POST "+issue+"/comments", local(m.handleAddIssueComment))
	mux.HandleFunc("POST "+issue+"/discuss", local(m.handleDiscussIssue))
	mux.HandleFunc("POST "+issue+"/transfer", local(m.handleFactoryIssueTransfer))
	mux.HandleFunc("POST "+issue+"/transfer/cancel", local(m.handleFactoryIssueTransferCancel))
	mux.HandleFunc("POST "+issue+"/reopen", local(m.handleReopenIssue))
	mux.HandleFunc("GET "+epic+"/control", m.handleGetControl)
	mux.HandleFunc("POST "+epic+"/control", local(m.handleEnsureControl))
	mux.HandleFunc("GET "+epic+"/control-actions/{action}", m.handleGetControlAction)
	mux.HandleFunc("POST "+epic+"/control-actions/{action}/{verb}", m.handleDecideControlAction)
	mux.HandleFunc("POST "+epic+"/mutations", local(m.handleMutateGraph))
	mux.HandleFunc("POST "+epic+"/pour", local(m.handlePour))
	mux.HandleFunc("POST "+epic+"/deliver-as-is", local(m.handleDeliverAsIs))
	mux.HandleFunc("POST "+epic+"/close", local(m.handleCloseEpic))
	mux.HandleFunc("POST "+epic+"/models", local(m.handleFactoryEpicModels))
	mux.HandleFunc("POST "+epic+"/pause", local(m.handleSetPaused(true)))
	mux.HandleFunc("POST "+epic+"/resume", local(m.handleSetPaused(false)))
	mux.HandleFunc("POST "+epic+"/mols/{mol}/close", local(m.handleCloseMol))
	mux.HandleFunc("POST "+epic+"/plans/{issue}", local(m.handleClaimPlan))
	mux.HandleFunc("POST "+epic+"/materializations/{issue}", local(m.handleMaterialize))
	mux.HandleFunc("POST "+epic+"/plan-gate/{action}", local(m.handleDecidePlanGate))
	mux.HandleFunc("GET "+epic+"/proposals", m.handleListProposals)
	mux.HandleFunc("POST "+epic+"/proposals", local(m.handleSubmitProposal))
	mux.HandleFunc("GET "+epic+"/proposals/{revision}", m.handleGetProposal)

	r.Handle("/api/factory/", m.deps.Guards.Auth(mux.ServeHTTP))
}
