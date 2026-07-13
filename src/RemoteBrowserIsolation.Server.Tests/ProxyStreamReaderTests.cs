using System.Text;
using RemoteBrowserIsolation.Server.Services.Proxy;

namespace RemoteBrowserIsolation.Server.Tests;

/// Tests for ProxyStreamReader: ReadLineAsync, ReadExactAsync, ReadByteAsync, DrainBuffered.
public class ProxyStreamReaderTests
{
    /// Helper stream that returns at most chunkSize bytes per ReadAsync call.
    private sealed class ChunkedReadStream : Stream
    {
        private readonly byte[] _data;
        private readonly int _chunkSize;
        private int _offset;

        public ChunkedReadStream(byte[] data, int chunkSize)
        {
            _data = data;
            _chunkSize = chunkSize;
            _offset = 0;
        }

        public override bool CanRead => true;
        public override bool CanSeek => false;
        public override bool CanWrite => false;
        public override long Length => throw new NotSupportedException();
        public override long Position
        {
            get => throw new NotSupportedException();
            set => throw new NotSupportedException();
        }

        public override void Flush() => throw new NotSupportedException();

        public override int Read(byte[] buffer, int offset, int count) =>
            throw new NotSupportedException();

        public override long Seek(long offset, SeekOrigin origin) =>
            throw new NotSupportedException();

        public override void SetLength(long value) =>
            throw new NotSupportedException();

        public override void Write(byte[] buffer, int offset, int count) =>
            throw new NotSupportedException();

        public override async Task<int> ReadAsync(byte[] buffer, int offset, int count,
            CancellationToken cancellationToken)
        {
            await Task.Yield();
            if (_offset >= _data.Length)
                return 0;

            int toRead = Math.Min(count, Math.Min(_chunkSize, _data.Length - _offset));
            Buffer.BlockCopy(_data, _offset, buffer, offset, toRead);
            _offset += toRead;
            return toRead;
        }
    }

    #region ReadLineAsync Tests

    [Fact]
    public async Task ReadLineAsync_CrlfTerminated_ReturnsLineWithoutCrlf()
    {
        var data = "Hello\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        var result = await reader.ReadLineAsync(CancellationToken.None);

        Assert.Equal("Hello", result);
    }

    [Fact]
    public async Task ReadLineAsync_BareLfTerminated_ReturnsLineWithoutLf()
    {
        var data = "Hello\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        var result = await reader.ReadLineAsync(CancellationToken.None);

        Assert.Equal("Hello", result);
    }

    [Fact]
    public async Task ReadLineAsync_EmptyLineWithCrlf_ReturnsEmptyString()
    {
        var data = "\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        var result = await reader.ReadLineAsync(CancellationToken.None);

        Assert.Equal("", result);
    }

    [Fact]
    public async Task ReadLineAsync_EmptyLineWithBareLf_ReturnsEmptyString()
    {
        var data = "\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        var result = await reader.ReadLineAsync(CancellationToken.None);

        Assert.Equal("", result);
    }

    [Fact]
    public async Task ReadLineAsync_NoTerminatorAtEof_ReturnsAccumulatedBytes()
    {
        var data = "Hello";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        var result = await reader.ReadLineAsync(CancellationToken.None);

        Assert.Equal("Hello", result);
    }

    [Fact]
    public async Task ReadLineAsync_CleanEofNoBytes_ReturnsNull()
    {
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(""));
        var reader = new ProxyStreamReader(stream);

        var result = await reader.ReadLineAsync(CancellationToken.None);

        Assert.Null(result);
    }

    [Fact]
    public async Task ReadLineAsync_TwoSuccessiveLines_BothReturnedCorrectly()
    {
        var data = "Line1\r\nLine2\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        var line1 = await reader.ReadLineAsync(CancellationToken.None);
        var line2 = await reader.ReadLineAsync(CancellationToken.None);

        Assert.Equal("Line1", line1);
        Assert.Equal("Line2", line2);
    }

    [Fact]
    public async Task ReadLineAsync_ThenReadExactAsync_ContinuesFromBufferedLeftover()
    {
        var data = "AB\nCDEF";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        var line = await reader.ReadLineAsync(CancellationToken.None);
        var exactBytes = await reader.ReadExactAsync(4, CancellationToken.None);

        Assert.Equal("AB", line);
        Assert.Equal("CDEF", Encoding.ASCII.GetString(exactBytes));
    }

    #endregion

    #region ReadExactAsync Tests

    [Fact]
    public async Task ReadExactAsync_ExactCount_ReturnsAllBytes()
    {
        var data = new byte[] { 1, 2, 3, 4, 5 };
        var stream = new MemoryStream(data);
        var reader = new ProxyStreamReader(stream);

        var result = await reader.ReadExactAsync(5, CancellationToken.None);

        Assert.Equal(new byte[] { 1, 2, 3, 4, 5 }, result);
    }

    [Fact]
    public async Task ReadExactAsync_ShortStream_ReturnsAvailableBytes()
    {
        var data = new byte[] { 1, 2, 3 };
        var stream = new MemoryStream(data);
        var reader = new ProxyStreamReader(stream);

        var result = await reader.ReadExactAsync(5, CancellationToken.None);

        Assert.Equal(new byte[] { 1, 2, 3 }, result);
    }

    [Fact]
    public async Task ReadExactAsync_EmptyStream_ReturnsEmptyArray()
    {
        var stream = new MemoryStream(new byte[] { });
        var reader = new ProxyStreamReader(stream);

        var result = await reader.ReadExactAsync(5, CancellationToken.None);

        Assert.Empty(result);
    }

    [Fact]
    public async Task ReadExactAsync_ZeroCount_ReturnsEmptyArray()
    {
        var data = new byte[] { 1, 2, 3, 4, 5 };
        var stream = new MemoryStream(data);
        var reader = new ProxyStreamReader(stream);

        var result = await reader.ReadExactAsync(0, CancellationToken.None);

        Assert.Empty(result);
    }

    [Fact]
    public async Task ReadExactAsync_BufferedThenStreamSpanning_ReadsAcrossFillBoundary()
    {
        var data = Encoding.ASCII.GetBytes("ABCDEFGH");
        var stream = new ChunkedReadStream(data, chunkSize: 3);
        var reader = new ProxyStreamReader(stream);

        var result = await reader.ReadExactAsync(8, CancellationToken.None);

        Assert.Equal("ABCDEFGH", Encoding.ASCII.GetString(result));
    }

    [Fact]
    public async Task ReadExactAsync_AfterReadLine_ConsumesRemainingBufferThenStream()
    {
        var data = Encoding.ASCII.GetBytes("AB\nCDEFGHIJ");
        var stream = new ChunkedReadStream(data, chunkSize: 4);
        var reader = new ProxyStreamReader(stream);

        var line = await reader.ReadLineAsync(CancellationToken.None);
        var exactBytes = await reader.ReadExactAsync(8, CancellationToken.None);

        Assert.Equal("AB", line);
        Assert.Equal("CDEFGHIJ", Encoding.ASCII.GetString(exactBytes));
    }

    #endregion

    #region ReadByteAsync Tests

    [Fact]
    public async Task ReadByteAsync_SingleByte_ReturnsByteValue()
    {
        var stream = new MemoryStream(new byte[] { 42 });
        var reader = new ProxyStreamReader(stream);

        var result = await reader.ReadByteAsync(CancellationToken.None);

        Assert.Equal(42, result);
    }

    [Fact]
    public async Task ReadByteAsync_EmptyStream_ReturnsNegativeOne()
    {
        var stream = new MemoryStream(new byte[] { });
        var reader = new ProxyStreamReader(stream);

        var result = await reader.ReadByteAsync(CancellationToken.None);

        Assert.Equal(-1, result);
    }

    [Fact]
    public async Task ReadByteAsync_ReadsSequentially_CorrectOrder()
    {
        var stream = new MemoryStream(new byte[] { 10, 20, 30 });
        var reader = new ProxyStreamReader(stream);

        var first = await reader.ReadByteAsync(CancellationToken.None);
        var second = await reader.ReadByteAsync(CancellationToken.None);
        var third = await reader.ReadByteAsync(CancellationToken.None);
        var eof = await reader.ReadByteAsync(CancellationToken.None);

        Assert.Equal(10, first);
        Assert.Equal(20, second);
        Assert.Equal(30, third);
        Assert.Equal(-1, eof);
    }

    [Fact]
    public async Task ReadByteAsync_AfterBufferExhausted_ReturnsNegativeOne()
    {
        var stream = new MemoryStream(new byte[] { 5 });
        var reader = new ProxyStreamReader(stream);

        var first = await reader.ReadByteAsync(CancellationToken.None);
        var second = await reader.ReadByteAsync(CancellationToken.None);

        Assert.Equal(5, first);
        Assert.Equal(-1, second);
    }

    #endregion

    #region DrainBuffered Tests

    [Fact]
    public void DrainBuffered_NoReadsPerformed_ReturnsEmptyArray()
    {
        var stream = new MemoryStream(Encoding.ASCII.GetBytes("Hello"));
        var reader = new ProxyStreamReader(stream);

        var result = reader.DrainBuffered();

        Assert.Empty(result);
    }

    [Fact]
    public async Task DrainBuffered_AfterReadLine_ReturnsUnconsumedBytes()
    {
        var data = "AB\nCDEF";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        await reader.ReadLineAsync(CancellationToken.None);
        var drained = reader.DrainBuffered();

        Assert.Equal("CDEF", Encoding.ASCII.GetString(drained));
    }

    [Fact]
    public async Task DrainBuffered_CalledTwice_SecondCallReturnsEmpty()
    {
        var data = "AB\nCDEF";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        await reader.ReadLineAsync(CancellationToken.None);
        var drained1 = reader.DrainBuffered();
        var drained2 = reader.DrainBuffered();

        Assert.Equal("CDEF", Encoding.ASCII.GetString(drained1));
        Assert.Empty(drained2);
    }

    [Fact]
    public async Task DrainBuffered_AfterAllBytesConsumed_ReturnsEmpty()
    {
        var data = "Hi\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        await reader.ReadLineAsync(CancellationToken.None);
        var drained = reader.DrainBuffered();

        Assert.Empty(drained);
    }

    [Fact]
    public async Task DrainBuffered_ThenSubsequentReadByte_ReturnsEof()
    {
        var data = "AB\nCD";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        await reader.ReadLineAsync(CancellationToken.None);
        reader.DrainBuffered();
        var nextByte = await reader.ReadByteAsync(CancellationToken.None);

        Assert.Equal(-1, nextByte);
    }

    #endregion
}
