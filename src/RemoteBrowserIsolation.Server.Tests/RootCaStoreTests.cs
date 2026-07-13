using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using RemoteBrowserIsolation.Server.Data;
using RemoteBrowserIsolation.Server.Data.Entities;
using RemoteBrowserIsolation.Server.Services.Proxy;

namespace RemoteBrowserIsolation.Server.Tests;

/// Tests for RootCaStore: GetActiveCaAsync loading and caching, Invalidate-forced reload, and
/// PKCS12 round-trip fidelity. Uses an EF Core in-memory database via IServiceScopeFactory, matching
/// the pattern in LogLevelSettingsStoreTests.cs and VideoEncoderSettingsStoreTests.cs.
public class RootCaStoreTests : IDisposable
{
    private readonly AppDbContext _db;
    private readonly IServiceScopeFactory _scopeFactory;

    public RootCaStoreTests()
    {
        var (scopeFactory, db) = CreateTestSetup();
        _scopeFactory = scopeFactory;
        _db = db;
    }

    public void Dispose()
    {
        _db.Dispose();
    }

    /// Builds an in-memory DB, returns the IServiceScopeFactory the store will use (scoped DbContext
    /// resolved per operation, exactly as in production) and a sibling AppDbContext that shares the
    /// same in-memory store so tests can seed rows and verify state.
    private static (IServiceScopeFactory scopeFactory, AppDbContext db) CreateTestSetup()
    {
        var dbName = Guid.NewGuid().ToString();

        var services = new ServiceCollection();
        services.AddDbContext<AppDbContext>(opts => opts.UseInMemoryDatabase(dbName));

        var provider = services.BuildServiceProvider();
        var scopeFactory = provider.GetRequiredService<IServiceScopeFactory>();

        var options = new DbContextOptionsBuilder<AppDbContext>()
            .UseInMemoryDatabase(dbName)
            .Options;
        var db = new AppDbContext(options);

        return (scopeFactory, db);
    }

    /// Builds a self-signed RSA root CA certificate. Uses 2048-bit key, matching LeafCertificateMinterTests.cs conventions.
    private static X509Certificate2 BuildSelfSignedCa(string dn = "CN=Test Root CA")
    {
        using var rsa = RSA.Create(2048);
        var subject = new X500DistinguishedName(dn);
        var request = new CertificateRequest(subject, rsa, HashAlgorithmName.SHA256, RSASignaturePadding.Pkcs1);
        request.CertificateExtensions.Add(
            new X509BasicConstraintsExtension(certificateAuthority: true, hasPathLengthConstraint: false, pathLengthConstraint: 0, critical: true));
        return request.CreateSelfSigned(DateTimeOffset.UtcNow.AddMinutes(-5), DateTimeOffset.UtcNow.AddYears(10));
    }

    /// Exports an X509Certificate2 (with private key) to a PKCS12 byte array plus the password used
    /// to protect it, so the result can be stored in a RootCertificateAuthority DB row.
    private static (byte[] pfxBytes, string pfxPassword) ExportToPkcs12(X509Certificate2 cert)
    {
        const string password = "test-pfx-password";
        return (cert.Export(X509ContentType.Pkcs12, password), password);
    }

    /// Inserts a RootCertificateAuthority row built from the given cert into the sibling DB context
    /// and returns the tracked entity (so callers can mutate/delete it in subsequent test steps).
    private async Task<RootCertificateAuthority> SeedCaRowAsync(X509Certificate2 cert)
    {
        var (pfxBytes, pfxPassword) = ExportToPkcs12(cert);
        var row = new RootCertificateAuthority
        {
            Subject = cert.Subject,
            NotBefore = cert.NotBefore,
            NotAfter = cert.NotAfter,
            Thumbprint = cert.Thumbprint,
            UploadedAt = DateTime.UtcNow,
            PfxBytes = pfxBytes,
            PfxPassword = pfxPassword
        };
        _db.RootCertificateAuthorities.Add(row);
        await _db.SaveChangesAsync();
        return row;
    }

    #region GetActiveCaAsync — empty DB

    [Fact]
    public async Task GetActiveCaAsync_NoRowInDb_ReturnsNull()
    {
        // Arrange — empty DB, no row seeded
        var store = new RootCaStore(_scopeFactory);

        // Act
        var result = await store.GetActiveCaAsync();

        // Assert — non-trivial: would return non-null if a row existed in the DB
        Assert.Null(result);
    }

    #endregion

    #region GetActiveCaAsync — caching

    [Fact]
    public async Task GetActiveCaAsync_RowExists_ReturnsNonNullCert()
    {
        // Arrange — seed a real CA so GetActiveCaAsync has something to load
        using var ca = BuildSelfSignedCa();
        await SeedCaRowAsync(ca);

        var store = new RootCaStore(_scopeFactory);

        // Act
        var result = await store.GetActiveCaAsync();

        // Assert — non-trivial: only passes if DB row exists AND PKCS12 bytes load correctly
        Assert.NotNull(result);
    }

    [Fact]
    public async Task GetActiveCaAsync_AfterFirstRead_ReturnsCachedValueIgnoringDbChange()
    {
        // Arrange — seed "First CA" and let the store load + cache it
        using var ca1 = BuildSelfSignedCa("CN=First CA");
        var row = await SeedCaRowAsync(ca1);

        var store = new RootCaStore(_scopeFactory);

        var first = await store.GetActiveCaAsync();
        // Guard: first call must have actually loaded a cert — fails if DB seeding broke
        Assert.NotNull(first);
        Assert.Contains("First CA", first.Subject);

        // Mutate the DB row to contain a completely different CA — proves the second call does NOT
        // re-read from the DB (caching), because if it did, it would return "Second CA" instead.
        using var ca2 = BuildSelfSignedCa("CN=Second CA");
        var (pfxBytes2, pfxPassword2) = ExportToPkcs12(ca2);
        row.Subject = ca2.Subject;
        row.PfxBytes = pfxBytes2;
        row.PfxPassword = pfxPassword2;
        await _db.SaveChangesAsync();

        // Act — second call; the caching guard: same object reference, still "First CA"
        var second = await store.GetActiveCaAsync();

        // Assert — non-trivial: would return "Second CA" (and Assert.Same would fail) if caching
        // were broken and every call re-read from the DB.
        Assert.Same(first, second);
        Assert.Contains("First CA", second!.Subject);
    }

    #endregion

    #region Invalidate — forces reload

    [Fact]
    public async Task GetActiveCaAsync_AfterInvalidate_ReloadsUpdatedRowFromDb()
    {
        // Arrange — seed "Original CA", load it into cache
        using var ca1 = BuildSelfSignedCa("CN=Original CA");
        var row = await SeedCaRowAsync(ca1);

        var store = new RootCaStore(_scopeFactory);

        var first = await store.GetActiveCaAsync();
        // Guard: initial load must have worked
        Assert.NotNull(first);
        Assert.Contains("Original CA", first.Subject);

        // Update the DB row to a replacement CA (caching test above proves that WITHOUT Invalidate
        // this change would be invisible to the store)
        using var ca2 = BuildSelfSignedCa("CN=Replacement CA");
        var (pfxBytes2, pfxPassword2) = ExportToPkcs12(ca2);
        row.Subject = ca2.Subject;
        row.PfxBytes = pfxBytes2;
        row.PfxPassword = pfxPassword2;
        await _db.SaveChangesAsync();

        // Act — invalidate then reload
        store.Invalidate();
        var second = await store.GetActiveCaAsync();

        // Assert — non-trivial: would still return "Original CA" if Invalidate() didn't clear the
        // cache (because the caching test proves that without Invalidate the old value persists).
        Assert.NotNull(second);
        Assert.Contains("Replacement CA", second.Subject);
    }

    [Fact]
    public async Task GetActiveCaAsync_AfterInvalidateAndRowDeleted_ReturnsNull()
    {
        // Arrange — seed a CA, load it, then delete the row and invalidate
        using var ca = BuildSelfSignedCa();
        var row = await SeedCaRowAsync(ca);

        var store = new RootCaStore(_scopeFactory);

        var first = await store.GetActiveCaAsync();
        // Guard: loaded a non-null cert before deletion
        Assert.NotNull(first);

        // Delete the only row in the table
        _db.RootCertificateAuthorities.Remove(row);
        await _db.SaveChangesAsync();

        // Act
        store.Invalidate();
        var second = await store.GetActiveCaAsync();

        // Assert — non-trivial: would return the stale cert if Invalidate() didn't clear the cache
        Assert.Null(second);
    }

    #endregion

    #region PKCS12 round-trip

    [Fact]
    public async Task GetActiveCaAsync_Pkcs12RoundTrip_ThumbprintMatchesOriginal()
    {
        // Arrange — create a real cert, export to PKCS12, store in DB, reload via RootCaStore
        using var ca = BuildSelfSignedCa("CN=RoundTrip CA");
        var expectedThumbprint = ca.Thumbprint;
        await SeedCaRowAsync(ca);

        var store = new RootCaStore(_scopeFactory);

        // Act
        var loaded = await store.GetActiveCaAsync();

        // Assert — non-trivial: fails if the PKCS12 bytes are corrupted or loaded with the wrong
        // password, or if Export/LoadPkcs12 silently alters the cert identity.
        Assert.NotNull(loaded);
        Assert.Equal(expectedThumbprint, loaded.Thumbprint);
    }

    [Fact]
    public async Task GetActiveCaAsync_Pkcs12RoundTrip_HasPrivateKey()
    {
        // Arrange — RootCaStore loads with X509KeyStorageFlags.Exportable; private key must survive
        // the Export → LoadPkcs12 round-trip so LeafCertificateMinter can sign with it.
        using var ca = BuildSelfSignedCa();
        await SeedCaRowAsync(ca);

        var store = new RootCaStore(_scopeFactory);

        // Act
        var loaded = await store.GetActiveCaAsync();

        // Assert — non-trivial: would be false if Export dropped the private key (e.g. wrong flags)
        // or if LoadPkcs12 was called without Exportable and the underlying CSP doesn't expose the key.
        Assert.NotNull(loaded);
        Assert.True(loaded.HasPrivateKey);
    }

    [Fact]
    public async Task GetActiveCaAsync_Pkcs12RoundTrip_SubjectMatchesOriginal()
    {
        // Arrange
        using var ca = BuildSelfSignedCa("CN=Subject Preservation CA");
        var expectedSubject = ca.Subject;
        await SeedCaRowAsync(ca);

        var store = new RootCaStore(_scopeFactory);

        // Act
        var loaded = await store.GetActiveCaAsync();

        // Assert — non-trivial: only passes if the X.509 distinguished name survives PKCS12 encoding
        Assert.NotNull(loaded);
        Assert.Equal(expectedSubject, loaded.Subject);
    }

    #endregion
}
