using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using Microsoft.EntityFrameworkCore;
using RemoteBrowserIsolation.Server.Data;
using RemoteBrowserIsolation.Server.Data.Entities;
using RemoteBrowserIsolation.Server.Models.Admin;
using RemoteBrowserIsolation.Server.Services.Proxy;

namespace RemoteBrowserIsolation.Server.Rest.Admin;

// CRUD for the single admin-uploaded root CA used by the TLS-intercepting proxy to mint leaf certs
// (see plans/9_TLS_proxy.md, LeafCertificateMinter). All routes require a valid bearer JWT.
public static class AdminRootCaEndpoints
{
    // Registers GET/POST/DELETE /api/admin/rootca and GET /api/admin/rootca/certificate.
    public static void MapAdminRootCaEndpoints(this WebApplication app)
    {
        RouteGroupBuilder group = app.MapGroup("/api/admin/rootca").RequireAuthorization();

        group.MapGet("", async (AppDbContext db) =>
        {
            RootCertificateAuthority? row = await db.RootCertificateAuthorities
                .AsNoTracking()
                .OrderByDescending(r => r.Id)
                .FirstOrDefaultAsync();

            return row is null
                ? Results.NotFound(new { error = "No root CA is currently configured." })
                : Results.Ok(ToResponse(row));
        });

        group.MapGet("/certificate", async (AppDbContext db) =>
        {
            RootCertificateAuthority? row = await db.RootCertificateAuthorities
                .AsNoTracking()
                .OrderByDescending(r => r.Id)
                .FirstOrDefaultAsync();

            if (row is null)
            {
                return Results.NotFound(new { error = "No root CA is currently configured." });
            }

            // Re-derive the public-only cert bytes from the stored PFX rather than persisting a
            // second copy -- the private key never leaves this handler.
            using X509Certificate2 cert = X509CertificateLoader.LoadPkcs12(row.PfxBytes, row.PfxPassword, X509KeyStorageFlags.Exportable);
            byte[] publicDer = cert.Export(X509ContentType.Cert);
            return Results.File(publicDer, "application/x-x509-ca-cert", "rbi-root-ca.cer");
        });

        group.MapPost("", async (HttpRequest request, AppDbContext db, IRootCaStore store) =>
        {
            if (!request.HasFormContentType)
            {
                return Results.BadRequest(new { error = "multipart/form-data with 'pfx' file and 'password' field is required." });
            }

            IFormCollection form = await request.ReadFormAsync();
            IFormFile? pfxFile = form.Files["pfx"];
            string password = form["password"].ToString();

            if (pfxFile is null || pfxFile.Length == 0)
            {
                return Results.BadRequest(new { error = "A 'pfx' file is required." });
            }

            byte[] pfxBytes;
            using (var buffer = new MemoryStream())
            {
                await pfxFile.CopyToAsync(buffer);
                pfxBytes = buffer.ToArray();
            }

            X509Certificate2 cert;
            try
            {
                cert = X509CertificateLoader.LoadPkcs12(pfxBytes, password, X509KeyStorageFlags.Exportable);
            }
            catch (Exception ex) when (ex is CryptographicException or ArgumentException)
            {
                return Results.BadRequest(new { error = "Could not parse the uploaded file as a PFX with the given password." });
            }

            using (cert)
            {
                if (!cert.HasPrivateKey)
                {
                    return Results.BadRequest(new { error = "The uploaded PFX has no private key. Upload a CA cert+key bundle, not a public-only certificate." });
                }

                X509BasicConstraintsExtension? basicConstraints = cert.Extensions
                    .OfType<X509BasicConstraintsExtension>()
                    .FirstOrDefault();
                if (basicConstraints is null || !basicConstraints.CertificateAuthority)
                {
                    return Results.BadRequest(new { error = "The uploaded certificate is not a CA (X509BasicConstraintsExtension.CertificateAuthority is false). Did you upload a leaf cert instead of a CA?" });
                }

                // Single active row: replace, don't accumulate history (see entity's doc comment).
                await db.RootCertificateAuthorities.ExecuteDeleteAsync();

                RootCertificateAuthority row = new RootCertificateAuthority
                {
                    Subject = cert.Subject,
                    NotBefore = cert.NotBefore.ToUniversalTime(),
                    NotAfter = cert.NotAfter.ToUniversalTime(),
                    Thumbprint = cert.Thumbprint,
                    UploadedAt = DateTime.UtcNow,
                    PfxBytes = pfxBytes,
                    PfxPassword = password,
                };
                db.RootCertificateAuthorities.Add(row);
                await db.SaveChangesAsync();

                store.Invalidate();
                return Results.Ok(ToResponse(row));
            }
        });

        group.MapDelete("", async (AppDbContext db, IRootCaStore store) =>
        {
            await db.RootCertificateAuthorities.ExecuteDeleteAsync();
            store.Invalidate();
            return Results.NoContent();
        });
    }

    private static RootCaResponse ToResponse(RootCertificateAuthority row)
    {
        return new RootCaResponse(row.Id, row.Subject, row.NotBefore, row.NotAfter, row.Thumbprint, row.UploadedAt);
    }
}
