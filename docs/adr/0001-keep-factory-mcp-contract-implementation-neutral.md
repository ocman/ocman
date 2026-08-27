# Keep the Factory MCP contract implementation-neutral

Factory MCP tools and the globally installed Factory skill expose only Work Epics, Planning Work, Formulas, acknowledgements, and Factory availability. Operational details remain available to users in Mission Control and logs, but are excluded from model-facing schemas, results, and errors so conversations cannot couple handoff behavior to Factory's replaceable storage implementation; the trade-off is that agents must direct users to Mission Control for detailed diagnostics.
