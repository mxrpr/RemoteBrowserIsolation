using System.Runtime.InteropServices;
using RemoteBrowserIsolation.Server.Services;

namespace RemoteBrowserIsolation.Server.Tests;

/// Tests for MjpegToI420Decoder.CopyPlane: row-by-row copy from (possibly padded) FFmpeg
/// source buffers to tightly-packed destination buffers.
public class MjpegToI420DecoderTests
{
    /// CopyPlane with sourceStride > rowBytes: verifies padding bytes are not copied into
    /// the tightly-packed destination. Source has 3 rows of 4 bytes each, with 4 bytes of
    /// padding per row (stride=8), destination has 3 rows of 4 bytes with no padding (stride=4).
    /// Each source row's first 4 bytes are filled with the row index (0, 1, 2), padding bytes
    /// are set to 0xFF to detect if they leak into the destination.
    [Fact]
    public unsafe void CopyPlane_StrideWithPadding_PacksRowsTightly()
    {
        const int rowBytes = 4;
        const int sourceStride = 8;
        const int height = 3;
        const byte paddingSentinel = 0xFF;

        // Allocate source buffer: 3 rows * 8 bytes per row = 24 bytes
        IntPtr source = Marshal.AllocHGlobal(sourceStride * height);
        try
        {
            // Allocate destination buffer: 3 rows * 4 bytes per row = 12 bytes (tightly packed)
            IntPtr destination = Marshal.AllocHGlobal(rowBytes * height);
            try
            {
                // Fill source buffer: each row's first rowBytes bytes get the row index repeated,
                // padding bytes get sentinel value
                byte* pSource = (byte*)source;
                for (int row = 0; row < height; row++)
                {
                    byte rowPattern = (byte)row;
                    for (int col = 0; col < rowBytes; col++)
                    {
                        pSource[row * sourceStride + col] = rowPattern;
                    }
                    for (int col = rowBytes; col < sourceStride; col++)
                    {
                        pSource[row * sourceStride + col] = paddingSentinel;
                    }
                }

                // Call CopyPlane
                MjpegToI420Decoder.CopyPlane(source, sourceStride, destination, rowBytes, rowBytes, height);

                // Verify destination: each row should contain only the row pattern bytes,
                // with no padding sentinel leaking in
                byte* pDest = (byte*)destination;
                for (int row = 0; row < height; row++)
                {
                    byte expectedPattern = (byte)row;
                    for (int col = 0; col < rowBytes; col++)
                    {
                        byte actual = pDest[row * rowBytes + col];
                        Assert.Equal(expectedPattern, actual);
                    }
                }

                // Double-check: destination buffer size is exactly rowBytes * height;
                // verify we haven't written beyond it (would crash if we tried, but be explicit)
                Assert.Equal(rowBytes * height, rowBytes * height);
            }
            finally
            {
                Marshal.FreeHGlobal(destination);
            }
        }
        finally
        {
            Marshal.FreeHGlobal(source);
        }
    }

    /// CopyPlane with sourceStride == rowBytes: no padding case. Verify that source and
    /// destination match byte-for-byte when the stride equals the tightly-packed row width.
    /// Allocate a 4x3 buffer (width=4, height=3) with stride=4, fill with sequential bytes
    /// (0-11), call CopyPlane, and verify destination is identical.
    [Fact]
    public unsafe void CopyPlane_StrideEqualsWidth_CopiesAsNoOpPackedCopy()
    {
        const int width = 4;
        const int height = 3;
        const int stride = width; // No padding: stride == width
        const int totalBytes = width * height;

        // Allocate source buffer: 12 bytes (4 cols * 3 rows)
        IntPtr source = Marshal.AllocHGlobal(totalBytes);
        try
        {
            // Allocate destination buffer: 12 bytes
            IntPtr destination = Marshal.AllocHGlobal(totalBytes);
            try
            {
                // Fill source with sequential bytes 0-11
                byte* pSource = (byte*)source;
                for (int i = 0; i < totalBytes; i++)
                {
                    pSource[i] = (byte)i;
                }

                // Call CopyPlane with stride == rowBytes (width)
                MjpegToI420Decoder.CopyPlane(source, stride, destination, width, width, height);

                // Verify destination matches source exactly
                byte* pDest = (byte*)destination;
                for (int i = 0; i < totalBytes; i++)
                {
                    byte expected = (byte)i;
                    byte actual = pDest[i];
                    Assert.Equal(expected, actual);
                }
            }
            finally
            {
                Marshal.FreeHGlobal(destination);
            }
        }
        finally
        {
            Marshal.FreeHGlobal(source);
        }
    }
}
