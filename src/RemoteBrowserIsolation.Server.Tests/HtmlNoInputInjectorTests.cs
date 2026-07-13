using System.Text;
using RemoteBrowserIsolation.Server.Services.Proxy;

namespace RemoteBrowserIsolation.Server.Tests;

/// Tests for HtmlNoInputInjector: charset normalization to UTF-8 and CSS injection for no-input mode.
public class HtmlNoInputInjectorTests
{
    private static byte[] Utf8(string html) => Encoding.UTF8.GetBytes(html);

    private static Uri AnyUrl => new Uri("https://example.com/");

    private static IHtmlNoInputInjector CreateInjector() => new HtmlNoInputInjector();

    #region Charset Normalization Tests

    [Fact]
    public void Process_ExistingCharsetMeta_NonUtf8_NormalizesToUtf8()
    {
        var injector = CreateInjector();
        var html = Utf8("<html><head><meta charset=\"iso-8859-1\"></head><body></body></html>");

        var result = injector.Process(html, AnyUrl, noInput: false);
        var output = Encoding.UTF8.GetString(result);

        Assert.Contains("charset=\"utf-8\"", output);
        Assert.DoesNotContain("iso-8859-1", output);
    }

    [Fact]
    public void Process_ExistingCharsetMeta_AlreadyUtf8_RemainsUtf8()
    {
        var injector = CreateInjector();
        var html = Utf8("<html><head><meta charset=\"utf-8\"></head><body></body></html>");

        var result = injector.Process(html, AnyUrl, noInput: false);
        var output = Encoding.UTF8.GetString(result);

        Assert.Contains("charset=\"utf-8\"", output);
        // Count occurrences of charset= to ensure only one
        int charsetCount = output.Split("charset=", StringSplitOptions.None).Length - 1;
        Assert.Equal(1, charsetCount);
    }

    [Fact]
    public void Process_ExistingCharsetMeta_UppercaseValue_NormalizesToLowercaseUtf8()
    {
        var injector = CreateInjector();
        var html = Utf8("<html><head><meta charset=\"UTF-8\"></head><body></body></html>");

        var result = injector.Process(html, AnyUrl, noInput: false);
        var output = Encoding.UTF8.GetString(result);

        // AngleSharp normalizes charset to lowercase
        Assert.Contains("charset=\"utf-8\"", output);
        Assert.DoesNotContain("UTF-8", output);
    }

    [Fact]
    public void Process_HttpEquivCharsetMeta_RemovesHttpEquivAndContent_SetsCharset()
    {
        var injector = CreateInjector();
        var html = Utf8("<html><head><meta http-equiv=\"Content-Type\" content=\"text/html; charset=windows-1252\"></head><body></body></html>");

        var result = injector.Process(html, AnyUrl, noInput: false);
        var output = Encoding.UTF8.GetString(result);

        Assert.Contains("charset=\"utf-8\"", output);
        Assert.DoesNotContain("http-equiv", output);
        Assert.DoesNotContain("content=\"text/html", output);
    }

    [Fact]
    public void Process_HttpEquivCharset_CaseInsensitiveHttpEquiv_Normalized()
    {
        var injector = CreateInjector();
        var html = Utf8("<html><head><meta http-equiv=\"content-type\" content=\"text/html; charset=iso-8859-1\"></head><body></body></html>");

        var result = injector.Process(html, AnyUrl, noInput: false);
        var output = Encoding.UTF8.GetString(result);

        Assert.Contains("charset=\"utf-8\"", output);
        Assert.DoesNotContain("http-equiv", output);
    }

    [Fact]
    public void Process_NoCharsetMeta_InsertsUtf8MetaAsFirstChildOfHead()
    {
        var injector = CreateInjector();
        var html = Utf8("<html><head><title>Test</title></head><body></body></html>");

        var result = injector.Process(html, AnyUrl, noInput: false);
        var output = Encoding.UTF8.GetString(result);

        Assert.Contains("charset=\"utf-8\"", output);
        int charsetIndex = output.IndexOf("charset=\"utf-8\"");
        int titleIndex = output.IndexOf("<title>");
        Assert.True(charsetIndex < titleIndex, "charset meta should appear before title");
    }

    [Fact]
    public void Process_EmptyHead_InsertsUtf8Meta()
    {
        var injector = CreateInjector();
        var html = Utf8("<html><head></head><body></body></html>");

        var result = injector.Process(html, AnyUrl, noInput: false);
        var output = Encoding.UTF8.GetString(result);

        Assert.Contains("charset=\"utf-8\"", output);
    }

    #endregion

    #region No-Input CSS Injection Tests

    [Fact]
    public void Process_NoInputTrue_InjectsStyleElementInHead()
    {
        var injector = CreateInjector();
        var html = Utf8("<html><head><meta charset=\"utf-8\"></head><body></body></html>");

        var result = injector.Process(html, AnyUrl, noInput: true);
        var output = Encoding.UTF8.GetString(result);

        Assert.Contains("<style", output);
        Assert.Contains("pointer-events:none!important", output);
        Assert.Contains("user-select:none!important", output);
        Assert.Contains("input,textarea,select", output);
    }

    [Fact]
    public void Process_NoInputFalse_DoesNotInjectStyleElement()
    {
        var injector = CreateInjector();
        var html = Utf8("<html><head><meta charset=\"utf-8\"></head><body></body></html>");

        var result = injector.Process(html, AnyUrl, noInput: false);
        var output = Encoding.UTF8.GetString(result);

        Assert.DoesNotContain("pointer-events:none", output);
        Assert.DoesNotContain("<style", output);
    }

    [Fact]
    public void Process_NoInputTrue_StyleAppearedInHead_NotInBody()
    {
        var injector = CreateInjector();
        var html = Utf8("<html><head><meta charset=\"utf-8\"></head><body><p>text</p></body></html>");

        var result = injector.Process(html, AnyUrl, noInput: true);
        var output = Encoding.UTF8.GetString(result);

        int styleIndex = output.IndexOf("pointer-events:none");
        int bodyIndex = output.IndexOf("<body");
        Assert.True(styleIndex < bodyIndex, "style should appear in head, before body");
    }

    #endregion

    #region UTF-8 Encoding Tests

    [Fact]
    public void Process_ReturnedBytes_AreUtf8_NoBom()
    {
        var injector = CreateInjector();
        var html = Utf8("<html><head><meta charset=\"utf-8\"></head><body><p>Test</p></body></html>");

        var result = injector.Process(html, AnyUrl, noInput: false);

        // Verify no BOM (UTF-8 BOM is 0xEF, 0xBB, 0xBF)
        Assert.False(result.Length >= 3 && result[0] == 0xEF && result[1] == 0xBB && result[2] == 0xBF,
            "Result should not start with UTF-8 BOM");

        // Verify it decodes without throwing
        var decoded = Encoding.UTF8.GetString(result);
        Assert.NotEmpty(decoded);
    }

    #endregion

    #region Combined Normalization and Injection Tests

    [Fact]
    public void Process_LegacyHttpEquivAndNoInputTrue_BothNormalizedAndStyleInjected()
    {
        var injector = CreateInjector();
        var html = Utf8("<html><head><meta http-equiv=\"Content-Type\" content=\"text/html; charset=iso-8859-1\"></head><body></body></html>");

        var result = injector.Process(html, AnyUrl, noInput: true);
        var output = Encoding.UTF8.GetString(result);

        // Verify charset normalization
        Assert.Contains("charset=\"utf-8\"", output);
        Assert.DoesNotContain("http-equiv", output);

        // Verify style injection
        Assert.Contains("pointer-events:none!important", output);
    }

    #endregion
}
