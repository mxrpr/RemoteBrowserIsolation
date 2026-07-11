using System.Text;

namespace RemoteBrowserIsolation.Server.Services.Proxy;

// Minimal buffered reader over a raw connection Stream (plain TCP or an already-established
// SslStream), purpose-built for parsing HTTP/1.1 request lines/headers/chunked bodies without
// depending on Kestrel -- Kestrel can't do "answer a plain CONNECT line, then start TLS on the same
// socket," which is the actual forward-proxy protocol (see plans/9_TLS_proxy.md). Buffers ahead for
// efficiency but exposes DrainBuffered so a caller that decides mid-read to blind-tunnel the
// connection (self-origin bypass, non-intercepted port) can hand any already-buffered-but-unconsumed
// bytes to the origin connection before splicing raw socket bytes -- otherwise those bytes would be
// silently dropped.
public sealed class ProxyStreamReader(Stream stream)
{
    private readonly byte[] _buffer = new byte[8192];
    private int _bufStart;
    private int _bufEnd;

    // Reads one CRLF- or bare-LF-terminated line (the newline itself is consumed but not included
    // in the result). Returns null only on a clean EOF before any byte of this line was read.
    public async Task<string?> ReadLineAsync(CancellationToken cancellationToken)
    {
        using var line = new MemoryStream();
        bool anyByte = false;
        while (true)
        {
            int b = await ReadByteAsync(cancellationToken);
            if (b < 0)
            {
                return anyByte ? Encoding.ASCII.GetString(line.ToArray()) : null;
            }

            anyByte = true;
            if (b == '\n')
            {
                byte[] bytes = line.ToArray();
                int len = bytes.Length > 0 && bytes[^1] == '\r' ? bytes.Length - 1 : bytes.Length;
                return Encoding.ASCII.GetString(bytes, 0, len);
            }

            line.WriteByte((byte)b);
        }
    }

    // Reads exactly count bytes, awaiting more from the stream as needed. If the stream closes
    // early, returns whatever was actually read (shorter than count) -- callers that need an exact
    // length guarantee must check the result length themselves.
    public async Task<byte[]> ReadExactAsync(int count, CancellationToken cancellationToken)
    {
        byte[] result = new byte[count];
        int filled = 0;
        while (filled < count)
        {
            if (_bufStart == _bufEnd)
            {
                await FillAsync(cancellationToken);
                if (_bufStart == _bufEnd)
                {
                    break;
                }
            }

            int take = Math.Min(count - filled, _bufEnd - _bufStart);
            Buffer.BlockCopy(_buffer, _bufStart, result, filled, take);
            _bufStart += take;
            filled += take;
        }

        return filled == count ? result : result[..filled];
    }

    public async Task<int> ReadByteAsync(CancellationToken cancellationToken)
    {
        if (_bufStart == _bufEnd)
        {
            await FillAsync(cancellationToken);
            if (_bufStart == _bufEnd)
            {
                return -1;
            }
        }

        return _buffer[_bufStart++];
    }

    // Returns (and clears) any bytes already pulled from the stream into the internal buffer but
    // not yet consumed by a caller. Must be written to the origin connection FIRST when switching
    // to a raw splice.
    public byte[] DrainBuffered()
    {
        if (_bufStart == _bufEnd)
        {
            return [];
        }

        byte[] result = _buffer[_bufStart.._bufEnd];
        _bufStart = _bufEnd;
        return result;
    }

    private async Task FillAsync(CancellationToken cancellationToken)
    {
        _bufStart = 0;
        _bufEnd = await stream.ReadAsync(_buffer, cancellationToken);
    }
}
