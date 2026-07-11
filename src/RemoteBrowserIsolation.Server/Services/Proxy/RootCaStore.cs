using System.Security.Cryptography.X509Certificates;
using Microsoft.EntityFrameworkCore;
using RemoteBrowserIsolation.Server.Data;
using RemoteBrowserIsolation.Server.Data.Entities;

namespace RemoteBrowserIsolation.Server.Services.Proxy;

// Holds the currently-active root CA (cert + private key) in memory so every proxy connection can
// read it without hitting the database per-request. Singleton -- same reasoning as
// WebRtcSessionManager's doc comment: this must survive across requests/connections and be shared,
// which a Scoped AppDbContext-backed service can't do. Refreshed explicitly on admin upload/delete
// (see AdminRootCaEndpoints) rather than polled, and lazily loaded from the DB on first use so a
// server restart picks the persisted CA back up without any extra startup wiring.
public interface IRootCaStore
{
    // The active CA, or null if none has been uploaded (or it was deleted). Callers that need a CA
    // to mint leaf certs (LeafCertificateMinter) must treat null as "proxy can't intercept TLS yet."
    Task<X509Certificate2?> GetActiveCaAsync(CancellationToken cancellationToken = default);

    // Forces the in-memory cert to be reloaded from the DB on next GetActiveCaAsync call. Called by
    // AdminRootCaEndpoints right after an upload/delete so the change takes effect without a
    // restart.
    void Invalidate();
}

public sealed class RootCaStore(IServiceScopeFactory scopeFactory) : IRootCaStore
{
    private readonly SemaphoreSlim _lock = new(1, 1);
    private X509Certificate2? _cached;
    private bool _loaded;

    public async Task<X509Certificate2?> GetActiveCaAsync(CancellationToken cancellationToken = default)
    {
        if (_loaded)
        {
            return _cached;
        }

        await _lock.WaitAsync(cancellationToken);
        try
        {
            if (_loaded)
            {
                return _cached;
            }

            // AppDbContext is Scoped; this singleton resolves a fresh scope per load, same pattern
            // Program.cs uses for the startup migration.
            using IServiceScope scope = scopeFactory.CreateScope();
            AppDbContext db = scope.ServiceProvider.GetRequiredService<AppDbContext>();
            RootCertificateAuthority? row = await db.RootCertificateAuthorities
                .AsNoTracking()
                .OrderByDescending(r => r.Id)
                .FirstOrDefaultAsync(cancellationToken);

            _cached = row is null
                ? null
                : X509CertificateLoader.LoadPkcs12(row.PfxBytes, row.PfxPassword, X509KeyStorageFlags.Exportable);
            _loaded = true;
            return _cached;
        }
        finally
        {
            _lock.Release();
        }
    }

    public void Invalidate()
    {
        _loaded = false;
        _cached = null;
    }
}
