namespace RemoteBrowserIsolation.Server.Models.Admin;

// Metadata-only shape of the active RootCertificateAuthority row, returned by GET /api/admin/rootca.
// Never carries PfxBytes/PfxPassword -- the private key must never leave the server.
public sealed record RootCaResponse(int Id, string Subject, DateTime NotBefore, DateTime NotAfter, string Thumbprint, DateTime UploadedAt);
