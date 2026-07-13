using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using RemoteBrowserIsolation.Server.Services.Proxy;

namespace RemoteBrowserIsolation.Server.Tests;

/// Tests for LeafCertificateMinter: leaf certificate minting and caching against a configurable root CA.
public class LeafCertificateMinterTests : IDisposable
{
    // Shared long-lived RSA CA certificate used across multiple tests
    private readonly X509Certificate2 _sharedCa;

    public LeafCertificateMinterTests()
    {
        // Build once per test class with 10-year validity
        _sharedCa = BuildSelfSignedCa(DateTimeOffset.UtcNow.AddMinutes(-5), DateTimeOffset.UtcNow.AddYears(10));
    }

    public void Dispose()
    {
        _sharedCa.Dispose();
    }

    /// Helper to build a self-signed root CA certificate with RSA key.
    private X509Certificate2 BuildSelfSignedCa(DateTimeOffset notBefore, DateTimeOffset notAfter)
    {
        using RSA rsa = RSA.Create(2048);
        var subject = new X500DistinguishedName("CN=Test Root CA");
        var request = new CertificateRequest(subject, rsa, HashAlgorithmName.SHA256, RSASignaturePadding.Pkcs1);

        request.CertificateExtensions.Add(
            new X509BasicConstraintsExtension(certificateAuthority: true, hasPathLengthConstraint: false, pathLengthConstraint: 0, critical: true));

        return request.CreateSelfSigned(notBefore, notAfter);
    }

    /// Helper to build a self-signed cert with controlled NotAfter for IsNearExpiry boundary tests.
    private X509Certificate2 BuildSelfSignedCert(DateTimeOffset notBefore, DateTimeOffset notAfter)
    {
        using RSA rsa = RSA.Create(2048);
        var subject = new X500DistinguishedName("CN=Test Cert");
        var request = new CertificateRequest(subject, rsa, HashAlgorithmName.SHA256, RSASignaturePadding.Pkcs1);

        return request.CreateSelfSigned(notBefore, notAfter);
    }

    /// Fake implementation of IRootCaStore for testing.
    private sealed class FakeRootCaStore(X509Certificate2? caToReturn) : IRootCaStore
    {
        public Task<X509Certificate2?> GetActiveCaAsync(CancellationToken cancellationToken = default)
        {
            return Task.FromResult(caToReturn);
        }

        public void Invalidate()
        {
            // No-op for testing
        }
    }

    #region IsNearExpiry Tests

    [Fact]
    public void IsNearExpiry_CertExpiresInTwoDays_ReturnsFalse()
    {
        // Create a cert that expires in 2 days (well beyond the 1-day renewal window)
        using var cert = BuildSelfSignedCert(DateTimeOffset.UtcNow, DateTimeOffset.UtcNow.AddDays(2));

        bool result = LeafCertificateMinter.IsNearExpiry(cert);

        Assert.False(result);
    }

    [Fact]
    public void IsNearExpiry_CertExpiresInTwelveHours_ReturnsTrue()
    {
        // Create a cert that expires in 12 hours (< 1 day, within renewal window)
        using var cert = BuildSelfSignedCert(DateTimeOffset.UtcNow, DateTimeOffset.UtcNow.AddHours(12));

        bool result = LeafCertificateMinter.IsNearExpiry(cert);

        Assert.True(result);
    }

    [Fact]
    public void IsNearExpiry_CertAlreadyExpired_ReturnsTrue()
    {
        // Create an expired cert (NotAfter is in the past)
        using var cert = BuildSelfSignedCert(DateTimeOffset.UtcNow.AddDays(-10), DateTimeOffset.UtcNow.AddMinutes(-5));

        bool result = LeafCertificateMinter.IsNearExpiry(cert);

        Assert.True(result);
    }

    #endregion

    #region GetOrMintAsync Tests

    [Fact]
    public async Task GetOrMintAsync_NoCaConfigured_ReturnsNull()
    {
        var store = new FakeRootCaStore(null);
        var minter = new LeafCertificateMinter(store);

        var result = await minter.GetOrMintAsync("example.com");

        Assert.Null(result);
    }

    [Fact]
    public async Task GetOrMintAsync_ValidRsaCa_ReturnsCertWithPrivateKey()
    {
        var store = new FakeRootCaStore(_sharedCa);
        var minter = new LeafCertificateMinter(store);

        var result = await minter.GetOrMintAsync("example.com");

        Assert.NotNull(result);
        Assert.True(result.HasPrivateKey);
    }

    [Fact]
    public async Task GetOrMintAsync_ValidRsaCa_SubjectCnMatchesHostname()
    {
        var store = new FakeRootCaStore(_sharedCa);
        var minter = new LeafCertificateMinter(store);

        var result = await minter.GetOrMintAsync("example.com");

        Assert.NotNull(result);
        Assert.Contains("CN=example.com", result.Subject);
    }

    [Fact]
    public async Task GetOrMintAsync_ValidRsaCa_SanContainsDnsHostname()
    {
        var store = new FakeRootCaStore(_sharedCa);
        var minter = new LeafCertificateMinter(store);

        var result = await minter.GetOrMintAsync("example.com");

        Assert.NotNull(result);
        var sanExt = result.Extensions["2.5.29.17"];
        Assert.NotNull(sanExt);
        string formattedSan = sanExt.Format(false);
        Assert.Contains("example.com", formattedSan);
    }

    [Fact]
    public async Task GetOrMintAsync_ValidRsaCa_HasServerAuthEku()
    {
        var store = new FakeRootCaStore(_sharedCa);
        var minter = new LeafCertificateMinter(store);

        var result = await minter.GetOrMintAsync("example.com");

        Assert.NotNull(result);
        var ekuExt = result.Extensions.OfType<X509EnhancedKeyUsageExtension>().FirstOrDefault();
        Assert.NotNull(ekuExt);
        Assert.Contains("1.3.6.1.5.5.7.3.1", ekuExt.EnhancedKeyUsages.OfType<Oid>().Select(o => o.Value));
    }

    [Fact]
    public async Task GetOrMintAsync_ValidRsaCa_BasicConstraintsNotCa()
    {
        var store = new FakeRootCaStore(_sharedCa);
        var minter = new LeafCertificateMinter(store);

        var result = await minter.GetOrMintAsync("example.com");

        Assert.NotNull(result);
        var bcExt = result.Extensions.OfType<X509BasicConstraintsExtension>().FirstOrDefault();
        Assert.NotNull(bcExt);
        Assert.False(bcExt.CertificateAuthority);
    }

    [Fact]
    public async Task GetOrMintAsync_ValidRsaCa_KeyUsageContainsDigitalSignatureAndKeyEncipherment()
    {
        var store = new FakeRootCaStore(_sharedCa);
        var minter = new LeafCertificateMinter(store);

        var result = await minter.GetOrMintAsync("example.com");

        Assert.NotNull(result);
        var kuExt = result.Extensions.OfType<X509KeyUsageExtension>().FirstOrDefault();
        Assert.NotNull(kuExt);
        Assert.True((kuExt.KeyUsages & X509KeyUsageFlags.DigitalSignature) != 0);
        Assert.True((kuExt.KeyUsages & X509KeyUsageFlags.KeyEncipherment) != 0);
    }

    [Fact]
    public async Task GetOrMintAsync_ValidRsaCa_LeafNotAfterDoesNotExceedCaNotAfter()
    {
        // Create a short-lived CA that expires in 30 minutes
        using var shortLivedCa = BuildSelfSignedCa(DateTimeOffset.UtcNow.AddMinutes(-5), DateTimeOffset.UtcNow.AddMinutes(30));
        var store = new FakeRootCaStore(shortLivedCa);
        var minter = new LeafCertificateMinter(store);

        var result = await minter.GetOrMintAsync("example.com");

        Assert.NotNull(result);
        Assert.True(result.NotAfter <= shortLivedCa.NotAfter);
    }

    [Fact]
    public async Task GetOrMintAsync_ValidRsaCa_LeafNotAfterIsApproximatelyNowPlusLeafValidity()
    {
        var store = new FakeRootCaStore(_sharedCa);
        var minter = new LeafCertificateMinter(store);

        // MintAsync backdates notBefore by 5 minutes (clock-skew tolerance) and adds the full
        // 7-day LeafValidity from there, so NotAfter lands ~5 minutes before UtcNow+7days.
        var beforeMint = DateTimeOffset.UtcNow.AddDays(7).AddMinutes(-5);
        var result = await minter.GetOrMintAsync("example.com");
        var afterMint = DateTimeOffset.UtcNow.AddDays(7).AddMinutes(-5);

        Assert.NotNull(result);
        // Allow ±5 second tolerance for execution time
        Assert.True(result.NotAfter >= beforeMint.AddSeconds(-5));
        Assert.True(result.NotAfter <= afterMint.AddSeconds(5));
    }

    [Fact]
    public async Task GetOrMintAsync_EcDsaCa_Throws()
    {
        // Create an ECDsa-only CA (no RSA private key)
        using ECDsa ecdsa = ECDsa.Create();
        var subject = new X500DistinguishedName("CN=ECDsa Test CA");
        var request = new CertificateRequest(subject, ecdsa, HashAlgorithmName.SHA256);

        request.CertificateExtensions.Add(
            new X509BasicConstraintsExtension(certificateAuthority: true, hasPathLengthConstraint: false, pathLengthConstraint: 0, critical: true));

        using X509Certificate2 ecdsaCa = request.CreateSelfSigned(DateTimeOffset.UtcNow.AddMinutes(-5), DateTimeOffset.UtcNow.AddYears(10));

        var store = new FakeRootCaStore(ecdsaCa);
        var minter = new LeafCertificateMinter(store);

        await Assert.ThrowsAsync<InvalidOperationException>(() => minter.GetOrMintAsync("example.com"));
    }

    [Fact]
    public async Task GetOrMintAsync_SameHostname_ReturnsCachedCert()
    {
        var store = new FakeRootCaStore(_sharedCa);
        var minter = new LeafCertificateMinter(store);

        var result1 = await minter.GetOrMintAsync("example.com");
        var result2 = await minter.GetOrMintAsync("example.com");

        Assert.NotNull(result1);
        Assert.NotNull(result2);
        Assert.Same(result1, result2);
    }

    [Fact]
    public async Task GetOrMintAsync_DifferentHostnames_ReturnDifferentCerts()
    {
        var store = new FakeRootCaStore(_sharedCa);
        var minter = new LeafCertificateMinter(store);

        var result1 = await minter.GetOrMintAsync("example.com");
        var result2 = await minter.GetOrMintAsync("other.com");

        Assert.NotNull(result1);
        Assert.NotNull(result2);
        Assert.NotSame(result1, result2);
        Assert.Contains("CN=example.com", result1.Subject);
        Assert.Contains("CN=other.com", result2.Subject);
    }

    [Fact]
    public async Task GetOrMintAsync_ClearCache_AfterMint_SubsequentCallReMints()
    {
        var store = new FakeRootCaStore(_sharedCa);
        var minter = new LeafCertificateMinter(store);

        var result1 = await minter.GetOrMintAsync("example.com");
        minter.ClearCache();
        var result2 = await minter.GetOrMintAsync("example.com");

        Assert.NotNull(result1);
        Assert.NotNull(result2);
        Assert.NotSame(result1, result2);
    }

    #endregion
}
