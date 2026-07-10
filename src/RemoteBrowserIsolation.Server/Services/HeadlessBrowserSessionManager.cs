using Microsoft.Playwright;

namespace RemoteBrowserIsolation.Server.Services;

// One server-side rendering session: an isolated BrowserContext + Page navigated to the target URL.
// Isolated per session so different users' sessions never share cookies/storage/state with each other.
public sealed record HeadlessSession(IBrowserContext Context, IPage Page);

public interface IHeadlessBrowserSessionManager
{
    Task<HeadlessSession> CreateSessionAsync(Uri targetUrl, int viewportWidth, int viewportHeight, CancellationToken cancellationToken = default);

    Task CloseSessionAsync(HeadlessSession session);
}

// Owns a single shared headless Chromium browser process for the whole server, launched lazily on
// first use, and hands out a fresh isolated BrowserContext+Page per WebRTC session. This is where
// untrusted target-page JavaScript actually executes — never on the client.
public sealed class HeadlessBrowserSessionManager : IHeadlessBrowserSessionManager, IAsyncDisposable
{
    private readonly ILogger<HeadlessBrowserSessionManager> logger;
    private readonly Lazy<Task<(IPlaywright Playwright, IBrowser Browser)>> browserInit;

    public HeadlessBrowserSessionManager(ILogger<HeadlessBrowserSessionManager> logger)
    {
        this.logger = logger;
        browserInit = new Lazy<Task<(IPlaywright, IBrowser)>>(InitBrowserAsync);
    }

    // Starts Playwright and launches headless Chromium. Runs once per process, the first time a
    // session is requested, since Playwright.CreateAsync/LaunchAsync can't run in a DI constructor.
    private static async Task<(IPlaywright Playwright, IBrowser Browser)> InitBrowserAsync()
    {
        var playwright = await Playwright.CreateAsync();
        var browser = await playwright.Chromium.LaunchAsync(new BrowserTypeLaunchOptions { Headless = true });
        return (playwright, browser);
    }

    // Opens a fresh isolated BrowserContext + Page for one WebRTC session, sized to the given
    // viewport, and navigates it to targetUrl.
    public async Task<HeadlessSession> CreateSessionAsync(Uri targetUrl, int viewportWidth, int viewportHeight, CancellationToken cancellationToken = default)
    {
        var (_, browser) = await browserInit.Value;
        // Viewport must equal the streamed frame size so client canvas coordinates map 1:1 to page
        // coordinates; both come from the same clamped client-requested size (see WebRtcSessionManager).
        var context = await browser.NewContextAsync(new BrowserNewContextOptions
        {
            ViewportSize = new ViewportSize { Width = viewportWidth, Height = viewportHeight },
        });
        var page = await context.NewPageAsync();
        await page.GotoAsync(targetUrl.ToString());
        return new HeadlessSession(context, page);
    }

    // Tears down one session's isolated context/page; the shared Browser process stays up for other sessions.
    public async Task CloseSessionAsync(HeadlessSession session)
    {
        await session.Context.CloseAsync();
    }

    // Shuts down the shared browser process and Playwright driver at application shutdown.
    public async ValueTask DisposeAsync()
    {
        if (!browserInit.IsValueCreated)
        {
            return;
        }

        var (playwright, browser) = await browserInit.Value;
        await browser.CloseAsync();
        playwright.Dispose();
        logger.LogInformation("Headless browser shut down");
    }
}
