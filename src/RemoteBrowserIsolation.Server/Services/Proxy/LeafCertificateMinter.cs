using System.Collections.Concurrent;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;

namespace RemoteBrowserIsolation.Server.Services.Proxy;

public interface ILeafCertificateMinter
{
    // Returns a cached or freshly-minted leaf certificate (with private key) for hostname, signed
    // by the active root CA -- or null if no CA is currently configured (RootCaStore.GetActiveCaAsync
    // returned null), which the caller must treat as "can't intercept TLS for this connection yet."
    // Async (not the plan's originally-sketched sync signature) because loading the CA is async --
    // see RootCaStore -- and this is called from the async TLS handshake path
    // (ServerOptionsSelectionCallback), not the older sync ServerCertificateSelectionCallback, so
    // there's no need to block on it.
    Task<X509Certificate2?> GetOrMintAsync(string hostname, CancellationToken cancellationToken = default);

    // Drops every cached leaf cert so the next request for any hostname re-mints against whatever
    // CA IRootCaStore currently holds. Must be called whenever the active root CA changes (upload
    // or delete) -- otherwise a hostname minted before the change keeps serving a leaf signed by
    // the stale CA for up to its full cache lifetime, regardless of how recently the CA changed.
    void ClearCache();
}

// Mints short-lived leaf certificates on demand, signed by the admin-uploaded root CA
// (IRootCaStore), for the TLS-intercepting proxy's SNI-based cert selection. Singleton: the mint
// cache must be shared and outlive any single proxy connection, same reasoning as RootCaStore.
//
// RSA-only: mints an RSA leaf and requires the active CA to itself hold an RSA private key
// (ca.GetRSAPrivateKey()). An uploaded CA with only an ECDsA key will fail to mint (documented
// limitation, not a bug -- RSA covers the overwhelming majority of self-signed/internal CAs an
// admin is likely to upload for this purpose).
public sealed class LeafCertificateMinter(IRootCaStore rootCaStore) : ILeafCertificateMinter
{
    // Re-mint once a cached leaf is within this long of its own expiry, so a long-lived proxy
    // process never hands out an already-expired cert.
    private static readonly TimeSpan RenewalWindow = TimeSpan.FromDays(1);

    // Leaf validity: short-lived by design (see class doc comment on the plan's reasoning) -- this
    // is a private trust anchor, not a publicly-trusted CA subject to CA/Browser Forum lifetime
    // limits.
    private static readonly TimeSpan LeafValidity = TimeSpan.FromDays(7);

    // No TTL eviction beyond the renewal-window re-mint above -- process-lifetime cache is fine per
    // plan; a server restart clears it naturally.
    private readonly ConcurrentDictionary<string, X509Certificate2> _cache = new(StringComparer.OrdinalIgnoreCase);

    // Serializes minting so two concurrent CONNECTs to the same brand-new host don't race to mint
    // (and cache-clobber) two different leaf certs. A single global lock, not per-host, is a
    // deliberate simplification given this project's expected connection volume.
    private readonly SemaphoreSlim _mintLock = new(1, 1);

    public async Task<X509Certificate2?> GetOrMintAsync(string hostname, CancellationToken cancellationToken = default)
    {
        if (_cache.TryGetValue(hostname, out X509Certificate2? cached) && !IsNearExpiry(cached))
        {
            return cached;
        }

        await _mintLock.WaitAsync(cancellationToken);
        try
        {
            // Re-check after acquiring the lock: another caller may have just minted this exact
            // hostname while this one was waiting.
            if (_cache.TryGetValue(hostname, out cached) && !IsNearExpiry(cached))
            {
                return cached;
            }

            X509Certificate2? minted = await MintAsync(hostname, cancellationToken);
            if (minted is null)
            {
                return null;
            }

            _cache[hostname] = minted;
            return minted;
        }
        finally
        {
            _mintLock.Release();
        }
    }

    public void ClearCache() => _cache.Clear();

    private static bool IsNearExpiry(X509Certificate2 cert) => cert.NotAfter - DateTime.Now < RenewalWindow;

    private async Task<X509Certificate2?> MintAsync(string hostname, CancellationToken cancellationToken)
    {
        X509Certificate2? ca = await rootCaStore.GetActiveCaAsync(cancellationToken);
        if (ca is null)
        {
            return null;
        }

        using RSA? caKey = ca.GetRSAPrivateKey();
        if (caKey is null)
        {
            throw new InvalidOperationException(
                "The active root CA has no RSA private key. LeafCertificateMinter only supports RSA-signed CAs.");
        }

        using RSA leafKey = RSA.Create(2048);
        var subject = new X500DistinguishedName($"CN={hostname}");
        var request = new CertificateRequest(subject, leafKey, HashAlgorithmName.SHA256, RSASignaturePadding.Pkcs1);

        // The single easiest thing to get wrong here (per plan): without a SAN DNS entry, modern
        // browsers reject the cert with ERR_CERT_COMMON_NAME_INVALID even though CN is set.
        var sanBuilder = new SubjectAlternativeNameBuilder();
        sanBuilder.AddDnsName(hostname);
        request.CertificateExtensions.Add(sanBuilder.Build());

        request.CertificateExtensions.Add(
            new X509KeyUsageExtension(X509KeyUsageFlags.DigitalSignature | X509KeyUsageFlags.KeyEncipherment, critical: true));

        // serverAuth EKU (1.3.6.1.5.5.7.3.1) -- required for browsers to accept this as a TLS server
        // certificate.
        request.CertificateExtensions.Add(
            new X509EnhancedKeyUsageExtension([new Oid("1.3.6.1.5.5.7.3.1")], critical: false));

        request.CertificateExtensions.Add(
            new X509BasicConstraintsExtension(certificateAuthority: false, hasPathLengthConstraint: false, pathLengthConstraint: 0, critical: true));

        byte[] serialNumber = RandomNumberGenerator.GetBytes(16);
        X509SignatureGenerator generator = X509SignatureGenerator.CreateForRSA(caKey, RSASignaturePadding.Pkcs1);

        DateTimeOffset notBefore = DateTimeOffset.UtcNow.AddMinutes(-5);
        DateTimeOffset notAfter = notBefore.Add(LeafValidity);
        // Never mint a leaf that outlives its own issuing CA.
        if (notAfter > ca.NotAfter)
        {
            notAfter = ca.NotAfter;
        }

        using X509Certificate2 leafPublicOnly = request.Create(ca.SubjectName, generator, notBefore, notAfter, serialNumber);

        // SslStream needs the leaf's private key present -- CopyWithPrivateKey attaches leafKey to
        // the signed-but-keyless cert Create() returned.
        return leafPublicOnly.CopyWithPrivateKey(leafKey);
    }
}
