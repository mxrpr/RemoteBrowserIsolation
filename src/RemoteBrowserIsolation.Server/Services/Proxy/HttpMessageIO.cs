using System.Globalization;
using System.Text;

namespace RemoteBrowserIsolation.Server.Services.Proxy;

// Request-line/header/body parsing and response-writing for the hand-rolled HTTP/1.1 tunnel path
// (plans/9_TLS_proxy.md's "tunnel parser spec"). Deliberately not a full RFC 7230 implementation --
// see that section's non-goals (obscure transfer-encodings, trailers, pipelining beyond one
// request/response per connection).
public static class HttpMessageIO
{
    public sealed record RequestLine(string Method, string Target, string HttpVersion);

    // Parses "METHOD target HTTP/x.y" -- returns null if the line doesn't have exactly three
    // space-separated tokens with an HTTP-version-looking last token.
    public static RequestLine? ParseRequestLine(string line)
    {
        string[] parts = line.Split(' ', 3);
        if (parts.Length != 3 || !parts[2].StartsWith("HTTP/", StringComparison.OrdinalIgnoreCase))
        {
            return null;
        }

        return new RequestLine(parts[0], parts[1], parts[2]);
    }

    // Reads headers until a blank line (CRLF CRLF), per RFC 7230 §3.2. No obsolete header-folding
    // support.
    public static async Task<List<ProxyHeader>> ReadHeadersAsync(ProxyStreamReader reader, CancellationToken cancellationToken)
    {
        List<ProxyHeader> headers = [];
        while (true)
        {
            string? line = await reader.ReadLineAsync(cancellationToken);
            if (string.IsNullOrEmpty(line))
            {
                break;
            }

            int colon = line.IndexOf(':');
            if (colon <= 0)
            {
                continue; // malformed header line -- skip rather than fail the whole request
            }

            headers.Add(new ProxyHeader(line[..colon].Trim(), line[(colon + 1)..].Trim()));
        }

        return headers;
    }

    // Determines body presence/length per the parser spec: Transfer-Encoding: chunked (if present)
    // takes priority over Content-Length, else Content-Length bytes, else no body.
    public static async Task<byte[]?> ReadBodyAsync(ProxyStreamReader reader, List<ProxyHeader> headers, CancellationToken cancellationToken)
    {
        bool chunked = headers.Any(h =>
            string.Equals(h.Name, "Transfer-Encoding", StringComparison.OrdinalIgnoreCase)
            && h.Value.Contains("chunked", StringComparison.OrdinalIgnoreCase));

        if (chunked)
        {
            return await ReadChunkedBodyAsync(reader, cancellationToken);
        }

        ProxyHeader? contentLengthHeader = headers.FirstOrDefault(h =>
            string.Equals(h.Name, "Content-Length", StringComparison.OrdinalIgnoreCase));
        if (contentLengthHeader is null
            || !int.TryParse(contentLengthHeader.Value, NumberStyles.None, CultureInfo.InvariantCulture, out int length)
            || length <= 0)
        {
            return null;
        }

        return await reader.ReadExactAsync(length, cancellationToken);
    }

    private static async Task<byte[]> ReadChunkedBodyAsync(ProxyStreamReader reader, CancellationToken cancellationToken)
    {
        using var body = new MemoryStream();
        while (true)
        {
            string? sizeLine = await reader.ReadLineAsync(cancellationToken);
            if (string.IsNullOrEmpty(sizeLine))
            {
                break;
            }

            // Chunk extensions (";name=value") are legal but unused here -- only the size before
            // ';' matters.
            string sizeToken = sizeLine.Split(';', 2)[0].Trim();
            if (!int.TryParse(sizeToken, NumberStyles.HexNumber, CultureInfo.InvariantCulture, out int chunkSize))
            {
                break;
            }

            if (chunkSize == 0)
            {
                // Zero-length chunk ends the body. Trailers are unsupported per the plan's
                // non-goals -- consume and drop the final CRLF.
                await reader.ReadLineAsync(cancellationToken);
                break;
            }

            byte[] chunk = await reader.ReadExactAsync(chunkSize, cancellationToken);
            body.Write(chunk);
            await reader.ReadLineAsync(cancellationToken); // trailing CRLF after each chunk's data
        }

        return body.ToArray();
    }

    // Writes a full HTTP/1.1 response: status line, hop-by-hop-stripped headers, an explicit
    // Content-Length (the body is always fully buffered by this point, and this project always
    // closes after one exchange -- see the parser spec on why chunked-out framing isn't used).
    public static async Task WriteResponseAsync(Stream stream, ProxyHttpResponse response, CancellationToken cancellationToken)
    {
        var sb = new StringBuilder();
        string reason = string.IsNullOrEmpty(response.ReasonPhrase) ? "OK" : response.ReasonPhrase;
        sb.Append("HTTP/1.1 ").Append(response.StatusCode).Append(' ').Append(reason).Append("\r\n");

        IEnumerable<string> connectionValues = response.Headers
            .Where(h => string.Equals(h.Name, "Connection", StringComparison.OrdinalIgnoreCase))
            .Select(h => h.Value);

        foreach (ProxyHeader header in response.Headers)
        {
            if (ProxyHeaders.IsHopByHop(header.Name, connectionValues)
                || string.Equals(header.Name, "Content-Length", StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }

            sb.Append(header.Name).Append(": ").Append(header.Value).Append("\r\n");
        }

        sb.Append("Content-Length: ").Append(response.Body.Length).Append("\r\n");
        // Always close after one exchange -- the "simplest safe choice for v1" the parser spec
        // calls out: correct but slower than real keep-alive, and it sidesteps needing unambiguous
        // request/response boundary tracking across multiple exchanges on one tunnel.
        sb.Append("Connection: close\r\n");
        sb.Append("\r\n");

        byte[] headerBytes = Encoding.ASCII.GetBytes(sb.ToString());
        await stream.WriteAsync(headerBytes, cancellationToken);
        if (response.Body.Length > 0)
        {
            await stream.WriteAsync(response.Body, cancellationToken);
        }

        await stream.FlushAsync(cancellationToken);
    }
}
