namespace RemoteBrowserIsolation.Server.Data.Entities;

// The single admin-uploaded CA used to mint per-hostname leaf certs for the TLS-intercepting proxy
// (see plans/9_TLS_proxy.md). Only one row is ever meaningful at a time -- uploading a new CA
// replaces it wholesale (AdminRootCaEndpoints deletes any existing row before inserting). No
// history/rotation table: this iteration doesn't support key rotation.
public sealed class RootCertificateAuthority
{
    public int Id { get; set; }

    public string Subject { get; set; } = string.Empty;

    public DateTime NotBefore { get; set; }

    public DateTime NotAfter { get; set; }

    public string Thumbprint { get; set; } = string.Empty;

    public DateTime UploadedAt { get; set; }

    // The full PFX (cert + private key), exactly as uploaded. RootCaStore is the only reader; never
    // exposed via any GET endpoint (AdminRootCaEndpoints only ever returns metadata or the
    // re-derived public certificate).
    public byte[] PfxBytes { get; set; } = [];

    // Password protecting PfxBytes, stored so RootCaStore can reload it (e.g. after a restart)
    // without re-prompting the admin. Same trust boundary as admin credentials already in this DB --
    // see plans/9_TLS_proxy.md's carried-over security note.
    public string PfxPassword { get; set; } = string.Empty;
}
