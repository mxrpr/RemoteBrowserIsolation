using System.Text;
using AngleSharp;
using AngleSharp.Dom;
using AngleSharp.Html;

namespace RemoteBrowserIsolation.Server.Services.Proxy;

public interface IHtmlNoInputInjector
{
    // Normalizes the document's charset meta to UTF-8 (matching the UTF-8 bytes this always
    // serializes) and, when noInput is true, injects a <style> that disables text entry. Returns
    // the bytes unchanged (well, re-serialized) either way -- callers only need to call this for
    // HtmlAllowInput/HtmlNoInput responses whose content-type is text/html.
    byte[] Process(byte[] htmlBytes, Uri pageUrl, bool noInput);
}

// HttpContext-free replacement for the HTML-mode-relay-era ContentRewriter (see
// plans/9_TLS_proxy.md): the transparent TLS-intercepting proxy makes URL-rewriting-to-a-relay-
// endpoint obsolete, since every request already goes through this server. All that survives from
// ContentRewriter is charset normalization and no-input <style> injection -- both salvaged here
// verbatim so a raw proxy socket connection (which has no HttpContext, hence no
// IHttpContextAccessor) can still apply them.
public sealed class HtmlNoInputInjector : IHtmlNoInputInjector
{
    // Exact CSS from ContentRewriter's NoInputStyleRule (originally iteration 5's
    // InjectNoInputStyle): scoped to text-capable controls so links/buttons/scroll keep working
    // under HtmlNoInput -- a blanket pointer-events:none on the whole document would also break
    // navigation, which policy_plan.md's "no input" definition doesn't ask for.
    private const string NoInputStyleRule =
        "input,textarea,select,[contenteditable],[contenteditable=\"true\"]{" +
        "pointer-events:none!important;user-select:none!important;-webkit-user-select:none!important;}";

    // AngleSharp's default configuration is immutable/side-effect-free and safe to share across
    // concurrent connections; a fresh IBrowsingContext is still created per parse since a context
    // tracks an "active document" and this service is registered as a singleton.
    private readonly AngleSharp.IConfiguration _angleSharpConfig = Configuration.Default;

    public byte[] Process(byte[] htmlBytes, Uri pageUrl, bool noInput)
    {
        IBrowsingContext context = BrowsingContext.New(_angleSharpConfig);
        using var input = new MemoryStream(htmlBytes);
        // No document loader is configured on _angleSharpConfig, so this does no network I/O of its
        // own and never fetches sub-resources during parsing -- OpenAsync's Task completes
        // synchronously here, so blocking on it is safe and keeps this method's signature
        // synchronous, matching the tunnel loop's synchronous per-request processing.
        IDocument document = context.OpenAsync(req => req.Content(input, shouldDispose: false).Address(pageUrl.ToString()))
            .GetAwaiter().GetResult();

        IElement? head = document.Head;
        if (head is not null)
        {
            NormalizeCharsetMeta(document, head);

            if (noInput)
            {
                IElement noInputStyle = document.CreateElement("style");
                noInputStyle.TextContent = NoInputStyleRule;
                head.AppendChild(noInputStyle);
            }
        }

        using var output = new MemoryStream();
        using (var writer = new StreamWriter(output, new UTF8Encoding(encoderShouldEmitUTF8Identifier: false), leaveOpen: true))
        {
            document.ToHtml(writer, HtmlMarkupFormatter.Instance);
        }
        return output.ToArray();
    }

    // Forces the document's declared charset to match the UTF-8 bytes this always serializes,
    // regardless of what the source page originally declared (HTML5 shorthand <meta charset> or the
    // legacy http-equiv Content-Type form -- both normalized to the shorthand form here).
    private static void NormalizeCharsetMeta(IDocument document, IElement head)
    {
        IElement? charsetMeta = null;
        foreach (IElement meta in head.QuerySelectorAll("meta"))
        {
            if (meta.HasAttribute("charset"))
            {
                charsetMeta = meta;
                break;
            }

            string? httpEquiv = meta.GetAttribute("http-equiv");
            if (httpEquiv is not null && httpEquiv.Equals("Content-Type", StringComparison.OrdinalIgnoreCase))
            {
                charsetMeta = meta;
                break;
            }
        }

        if (charsetMeta is not null)
        {
            charsetMeta.RemoveAttribute("http-equiv");
            charsetMeta.RemoveAttribute("content");
            charsetMeta.SetAttribute("charset", "utf-8");
        }
        else
        {
            IElement meta = document.CreateElement("meta");
            meta.SetAttribute("charset", "utf-8");
            head.InsertBefore(meta, head.FirstChild);
        }
    }
}
