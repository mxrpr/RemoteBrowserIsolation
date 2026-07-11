using RemoteBrowserIsolation.Server.Models;

namespace RemoteBrowserIsolation.Server.Services;

public interface IPolicyEngine
{
    // Resolves the ViewMode an admin has authorized for the given URL's host, or null if no
    // SitePolicy row matches — null means deny. Not yet called from the browse endpoints (that's a
    // later pass); exists so PolicyEngine can be exercised and reviewed independently.
    Task<ViewMode?> ResolveAsync(Uri url, CancellationToken cancellationToken = default);
}
