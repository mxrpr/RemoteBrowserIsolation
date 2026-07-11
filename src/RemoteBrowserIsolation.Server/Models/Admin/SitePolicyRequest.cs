namespace RemoteBrowserIsolation.Server.Models.Admin;

// POST/PUT /api/admin/sites body — the admin-editable fields of a SitePolicy. HostPattern is a bare
// host (e.g. "example.com"); PolicyEngine matches it and its subdomains.
public sealed record SitePolicyRequest(string HostPattern, ViewMode ViewMode);
