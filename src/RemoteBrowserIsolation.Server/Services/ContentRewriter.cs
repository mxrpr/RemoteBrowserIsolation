using System.Text;
using System.Text.Json;
using System.Text.RegularExpressions;
using AngleSharp;
using AngleSharp.Dom;
using AngleSharp.Html;

namespace RemoteBrowserIsolation.Server.Services;

public interface IContentRewriter
{
    byte[] RewriteHtml(byte[] htmlBytes, Uri pageUrl, bool noInput);
    byte[] RewriteCss(byte[] cssBytes, Uri stylesheetUrl);
    byte[] RewriteJavaScript(byte[] jsBytes, Uri scriptUrl);
}

// Rewrites HTML/CSS responses so every internal link and sub-resource URL routes back through
// /api/session/fetch instead of resolving against our own origin (404s) or escaping to the live
// site directly (bypasses the relay, triggers the target's own X-Frame-Options/CSP). See
// plans/6_iteration_html_rewrite_proxy.md for the live bug (origo.hu) this fixes. Every rewritten
// URL re-enters the same policy-gated /api/session/fetch endpoint, so a third-party host with no
// SitePolicy row still 403s per-asset -- that's intentional default-deny, not a bug to route around.
public sealed class ContentRewriter(IHttpContextAccessor httpContextAccessor) : IContentRewriter
{
    // Elements/attributes this pass rewrites, per the iteration-6 plan's in-scope list. <a>/<link>
    // carry their URL in href; the rest carry it in src.
    private const string HrefSelector = "a[href],link[href]";
    private const string SrcSelector = "img[src],script[src],source[src],video[src],audio[src],iframe[src]";

    // Exact CSS from iteration 5's InjectNoInputStyle, reused verbatim per the plan: scoped to
    // text-capable controls so links/buttons/scroll keep working under HtmlNoInput -- a blanket
    // pointer-events:none on the whole document would also break navigation, which policy_plan.md's
    // "no input" definition doesn't ask for.
    private const string NoInputStyleRule =
        "input,textarea,select,[contenteditable],[contenteditable=\"true\"]{" +
        "pointer-events:none!important;user-select:none!important;-webkit-user-select:none!important;}";

    // url(...) in its three quoting styles (unquoted/single/double), and both common @import forms
    // (`@import url(...)` and `@import "...";`). A full CSS parser is out of scope for this
    // iteration per the plan -- this is a documented pragmatic gap: it can be fooled by url()-like
    // text sitting inside a CSS comment or string literal that isn't actually a reference, which is
    // rare enough in real stylesheets to accept.
    private static readonly Regex UrlFunctionRegex = new(@"url\(\s*(['""]?)(?<url>[^'"")]+)\1\s*\)", RegexOptions.Compiled | RegexOptions.IgnoreCase);
    private static readonly Regex ImportStringRegex = new(@"@import\s+(['""])(?<url>[^'""]+)\1", RegexOptions.Compiled | RegexOptions.IgnoreCase);

    // Three patterns for RewriteJavaScript (iteration 8), each targeting a syntactically distinct,
    // mutually-exclusive shape so a single "import" token can only ever be matched by one of them --
    // see that method's comment for why this is safe against the false-positive risk a naive "match
    // any string after the word from" scan would have.
    //   1. import("...") / import('...')                         -- dynamic import call
    //   2. import"..." / import "..."                             -- bare side-effect-only import
    //      (no bindings, no "from" -- confirmed live in origo.hu's own bundle output, e.g.
    //      `import"./chunk-3GBFCLWG.js";` with zero surrounding whitespace)
    //   3. ...from"..." / ...from '...'                           -- the specifier half of BOTH
    //      `import <bindings> from "..."` and `export <bindings> from "..."`. Deliberately does not
    //      try to also match the binding-list half (default/named/namespace/mixed) since "from" is a
    //      contextual keyword that's only ever legally followed directly by a string literal in an
    //      import/export declaration -- nowhere else in JS grammar can an identifier abut a string
    //      literal with no operator between them -- so matching just "from<quote>" is both simpler
    //      and safe against Array.from("x")/Rx from("x") (always followed by "(", not a quote) and
    //      object keys like {from:"x"} (always followed by ":", not a quote) directly.
    //
    // Both false-positive guards below were found live, not theorized: origo.hu's own bundle embeds
    // rrweb (a session-replay lib pulled in via Sentry) which contains the literal JS string
    // "@import" (building CSS text at runtime: `["@import", \`url(...)\`]`). "@" is a non-word char,
    // so a bare \bimport would match "import" INSIDE that string literal, treat the string's own
    // closing quote as the specifier's opening quote, and consume everything up to the next quote in
    // the file as a bogus "URL" -- corrupting unrelated code into a syntax error. The negative
    // lookbehind below excludes every character that can legally precede "import" only as part of a
    // larger token/string (word char, @, quote, backtick, dot) while still allowing every real
    // statement/expression-boundary character (;, (, {, whitespace, start-of-file, etc.) that
    // precedes a genuine import keyword. FromClauseRegex gets a matching lookahead instead: a real
    // `from"..."` specifier is always immediately followed by the statement terminator (confirmed
    // live: every from-clause in origo.hu's bundle is followed directly by `;`), which is the
    // cheapest way to avoid matching plain English text that happens to end in the word "from" right
    // before an unrelated closing quote.
    private static readonly Regex DynamicImportRegex = new(@"(?<![\w@'""`.])import\s*\(\s*(['""])(?<url>[^'""]+)\1\s*\)", RegexOptions.Compiled);
    private static readonly Regex BareImportRegex = new(@"(?<![\w@'""`.])import\s*(['""])(?<url>[^'""]+)\1", RegexOptions.Compiled);
    private static readonly Regex FromClauseRegex = new(@"\bfrom\s*(['""])(?<url>[^'""]+)\1(?=\s*(?:;|,|\)|$))", RegexOptions.Compiled);

    // Single source of truth for pseudo-schemes that are never real cross-origin-fetchable
    // resources. Shared by TryResolveNavigableUrl (server-side attribute rewrite) AND
    // BuildRuntimeRewriteShim (the JS emitted below, iteration 7) so the two passes can't drift
    // apart -- the JS regex is generated from this exact array rather than hand-typed a second time.
    private static readonly string[] SkipUrlPrefixes = ["javascript:", "data:", "blob:", "mailto:", "tel:", "about:"];

    // The relay path every rewritten URL is re-pointed at, both server-side (BuildFetchUrl) and at
    // runtime (the JS shim below) -- kept as one constant so the two can't silently diverge.
    private const string FetchRelayPath = "/api/session/fetch?url=";

    // AngleSharp's default configuration is immutable/side-effect-free and safe to share across
    // concurrent requests; a fresh IBrowsingContext is still created per parse in RewriteHtml since a
    // context tracks an "active document" and this service is registered as a singleton (see
    // Program.cs) called concurrently.
    private readonly AngleSharp.IConfiguration _angleSharpConfig = Configuration.Default;

    // Parses the page with AngleSharp (fed the raw byte stream, not a pre-decoded string, so it runs
    // its real charset sniff -- BOM, then <meta charset>/http-equiv prescan -- instead of iteration
    // 5's naive Encoding.UTF8.GetString, which silently mangled non-UTF-8 pages), rewrites every
    // in-scope URL to route back through /api/session/fetch, and always re-serializes as UTF-8.
    public byte[] RewriteHtml(byte[] htmlBytes, Uri pageUrl, bool noInput)
    {
        Uri serverOrigin = GetServerOrigin();
        IBrowsingContext context = BrowsingContext.New(_angleSharpConfig);
        using var input = new MemoryStream(htmlBytes);
        // No document loader is configured on _angleSharpConfig, so this does no network I/O of its
        // own (the content is supplied directly) and never fetches sub-resources during parsing --
        // OpenAsync's Task completes synchronously here, so blocking on it is safe and keeps this
        // method's signature matching the plan's synchronous IContentRewriter interface.
        IDocument document = context.OpenAsync(req => req.Content(input, shouldDispose: false).Address(pageUrl.ToString()))
            .GetAwaiter().GetResult();

        foreach (IElement element in document.QuerySelectorAll(HrefSelector))
        {
            RewriteUrlAttribute(element, "href", pageUrl, serverOrigin);
        }

        foreach (IElement element in document.QuerySelectorAll(SrcSelector))
        {
            RewriteUrlAttribute(element, "src", pageUrl, serverOrigin);
        }

        // Inline <style> blocks go through the same url()/@import rewrite as standalone CSS
        // responses (RewriteCss) since their content is CSS text either way.
        foreach (IElement styleElement in document.QuerySelectorAll("style"))
        {
            styleElement.TextContent = RewriteCssText(styleElement.TextContent ?? string.Empty, pageUrl, serverOrigin);
        }

        IElement? head = document.Head;
        if (head is not null)
        {
            // Belt-and-suspenders fallback (per plan), not the primary fix: anything the element
            // pass above doesn't catch -- e.g. a relative URL added dynamically by inline JS before
            // any script rewrite could matter -- still resolves against the real page URL instead of
            // our own origin. It does NOT route such URLs back through the relay (out of scope, see
            // plan's non-goals), so this only prevents 404s, not relay escapes, for the cases it
            // covers.
            //
            // Important interaction this class must get right: once this <base> tag is present, ANY
            // root-relative URL left in the document (including our own rewritten ones, if they were
            // written as bare "/api/session/fetch?...") would resolve against pageUrl's origin
            // instead of ours -- i.e. straight back to the live site, recreating the exact "escapes
            // the relay" bug this iteration fixes. That's why BuildFetchUrl below always emits a
            // fully-qualified absolute URL against our own server origin: absolute URLs are immune to
            // <base>. Caught live via a Playwright run against origo.hu during verification (requests
            // were observed going to https://origo.hu/api/session/fetch?... instead of this server).
            IElement? baseElement = head.QuerySelector("base");
            if (baseElement is null)
            {
                baseElement = document.CreateElement("base");
                head.InsertBefore(baseElement, head.FirstChild);
            }
            baseElement.SetAttribute("href", pageUrl.ToString());

            NormalizeCharsetMeta(document, head);

            if (noInput)
            {
                IElement noInputStyle = document.CreateElement("style");
                noInputStyle.TextContent = NoInputStyleRule;
                head.AppendChild(noInputStyle);
            }

            // Must be the LAST "insert at head.FirstChild" operation in this method (see
            // plans/7_iteration_runtime_rewrite_shim.md): InsertBefore(x, head.FirstChild) always
            // makes x the new first child regardless of what's already there, so doing this after
            // the <base>/charset-meta inserts above -- rather than before -- is what actually
            // guarantees the shim ends up ahead of them (and ahead of every one of the page's own
            // <script>/<link> tags) in document order. HTML parses and executes synchronous inline
            // scripts in source order, so "true first child of <head>" is what makes the monkey-patch
            // active before anything else on the page can call fetch/XHR/set src or href.
            IElement shimScript = document.CreateElement("script");
            shimScript.TextContent = BuildRuntimeRewriteShim(pageUrl);
            head.InsertBefore(shimScript, head.FirstChild);
        }

        using var output = new MemoryStream();
        using (var writer = new StreamWriter(output, new UTF8Encoding(encoderShouldEmitUTF8Identifier: false), leaveOpen: true))
        {
            document.ToHtml(writer, HtmlMarkupFormatter.Instance);
        }
        return output.ToArray();
    }

    // Generates the iteration-7 runtime shim: a small inline <script> that monkey-patches
    // fetch/XHR/element src+href so URLs a single-page app adds AFTER the initial parse (e.g. an
    // Angular router's lazy-loaded route chunk) still route back through /api/session/fetch instead
    // of resolving straight to the live origin -- RewriteHtml's element/attribute pass above only
    // catches what's present in the initial HTML response. See
    // plans/7_iteration_runtime_rewrite_shim.md for the origo.hu bug this closes (confirmed live: a
    // post-click chunk request bypassed the relay and hit www.origo.hu directly). Known, disclosed
    // gap: native ESM dynamic import() (`<script type="module">` lazy imports) has no
    // monkey-patchable browser hook and is intentionally NOT caught here -- see the plan's non-goals.
    private static string BuildRuntimeRewriteShim(Uri pageUrl)
    {
        // JSON-serialized (not hand-quoted) so a page URL containing a quote, backslash, or a
        // "</script>"-shaped substring in its query string can't break out of the inline <script>
        // block or the JS string literal -- System.Text.Json's default encoder escapes '<'/'>'/'&' as
        // \uXXXX, which also neutralizes any literal "</script" sequence embedded in the URL.
        string realUrlJson = JsonSerializer.Serialize(pageUrl.ToString());

        // Built from the exact same SkipUrlPrefixes array TryResolveNavigableUrl uses server-side
        // (see that field's comment above) so the two skip-lists can't silently drift apart.
        string skipPattern = string.Join("|", SkipUrlPrefixes.Select(Regex.Escape));

        return """
        (function () {
          var REAL_URL = __REAL_URL_JSON__;
          var SKIP_RE = /^(__SKIP_PATTERN__)/i;
          var RELAY_MARKER = '__RELAY_PATH__';

          function relayPrefix() { return location.protocol + '//' + location.host + RELAY_MARKER; }

          // Not a real cross-origin-fetchable URL (pseudo-scheme, fragment-only, empty), or already
          // pointed at our relay -- the latter check is what keeps this idempotent: code that reads a
          // src/href back (already rewritten by us) and reassigns the same value must not get
          // double-wrapped into .../api/session/fetch?url=.../api/session/fetch?url=....
          function shouldSkip(url) {
            if (!url) return true;
            var s = String(url);
            return SKIP_RE.test(s) || s.charAt(0) === '#' || s.indexOf(RELAY_MARKER) !== -1;
          }

          function rewrite(url) {
            if (shouldSkip(url)) return url;
            try {
              var abs = new URL(url, REAL_URL).href;
              return relayPrefix() + encodeURIComponent(abs);
            } catch (e) {
              return url;
            }
          }

          var origFetch = window.fetch;
          if (origFetch) {
            window.fetch = function (input, init) {
              // Request-object form (fetch(new Request(url))) needs its URL swapped via
              // `new Request(newUrl, input)` since Request.url is read-only. Passing a Request as the
              // second (init) argument is spec-legal: the Request constructor reads back
              // method/headers/body/mode/credentials/etc. from it as a plain dictionary, so this
              // preserves everything about the original request except the URL itself.
              if (typeof input === 'string' || input instanceof URL) {
                return origFetch.call(this, rewrite(String(input)), init);
              }
              if (input && typeof input.url === 'string') {
                return origFetch.call(this, new Request(rewrite(input.url), input), init);
              }
              return origFetch.call(this, input, init);
            };
          }

          var origOpen = XMLHttpRequest.prototype.open;
          XMLHttpRequest.prototype.open = function (method, url) {
            var args = Array.prototype.slice.call(arguments);
            args[1] = rewrite(url);
            return origOpen.apply(this, args);
          };

          // setAttribute alone misses `el.src = '...'` / `el.href = '...'` property assignment,
          // which real-world lazy-loaders use at least as often as setAttribute -- both paths are
          // patched so neither one is a way to sneak a URL past this shim.
          var origSetAttribute = Element.prototype.setAttribute;
          Element.prototype.setAttribute = function (name, value) {
            var lname = String(name).toLowerCase();
            if ((lname === 'src' || lname === 'href') && /^(SCRIPT|IMG|LINK|IFRAME|SOURCE|VIDEO|AUDIO)$/.test(this.tagName)) {
              value = rewrite(value);
            }
            return origSetAttribute.call(this, name, value);
          };

          // Property-setter patch, per prototype rather than Element.prototype: src/href accessors
          // only exist at these specific prototype levels. Verified against this project's actual
          // bundled Chromium build (not assumed from memory, per plan) that each pair below has an
          // own getter/setter at exactly this level; HTMLMediaElement.prototype's pair is inherited by
          // both HTMLVideoElement and HTMLAudioElement automatically via the prototype chain, so
          // neither needs (or gets) a separate entry.
          [[HTMLScriptElement, 'src'], [HTMLImageElement, 'src'], [HTMLLinkElement, 'href'],
           [HTMLIFrameElement, 'src'], [HTMLSourceElement, 'src'], [HTMLMediaElement, 'src']]
            .forEach(function (pair) {
              var proto = pair[0] && pair[0].prototype, attr = pair[1];
              if (!proto) return;
              var desc = Object.getOwnPropertyDescriptor(proto, attr);
              if (!desc || !desc.set || !desc.get) return;
              Object.defineProperty(proto, attr, {
                get: desc.get,
                set: function (v) { desc.set.call(this, rewrite(v)); },
                configurable: true,
              });
            });
        })();
        """
            .Replace("__REAL_URL_JSON__", realUrlJson)
            .Replace("__SKIP_PATTERN__", skipPattern)
            .Replace("__RELAY_PATH__", FetchRelayPath);
    }

    // Regex-based url()/@import rewrite for a standalone text/css response. No charset sniffing here
    // -- PageDownloader doesn't expose the response's charset, and CSS's own @charset-rule detection
    // would need infrastructure AngleSharp only gives us for HTML -- so this assumes UTF-8, matching
    // this project's pre-iteration-6 behavior and the overwhelming real-world default for CSS.
    public byte[] RewriteCss(byte[] cssBytes, Uri stylesheetUrl)
    {
        Uri serverOrigin = GetServerOrigin();
        string css = Encoding.UTF8.GetString(cssBytes);
        string rewritten = RewriteCssText(css, stylesheetUrl, serverOrigin);
        return Encoding.UTF8.GetBytes(rewritten);
    }

    // Shared by RewriteCss and RewriteHtml's inline <style> handling. Order matters: the url()
    // regex runs first and fully handles `@import url(...)`, so the second regex only ever matches
    // the separate `@import "...";` string form -- no double-rewrite of the same reference.
    private static string RewriteCssText(string css, Uri baseUrl, Uri serverOrigin)
    {
        string result = UrlFunctionRegex.Replace(css, match =>
        {
            string raw = match.Groups["url"].Value.Trim();
            return TryResolveNavigableUrl(raw, baseUrl, out Uri? absolute)
                ? $"url(\"{BuildFetchUrl(absolute!, serverOrigin)}\")"
                : match.Value;
        });

        return ImportStringRegex.Replace(result, match =>
        {
            string raw = match.Groups["url"].Value.Trim();
            return TryResolveNavigableUrl(raw, baseUrl, out Uri? absolute)
                ? $"@import \"{BuildFetchUrl(absolute!, serverOrigin)}\""
                : match.Value;
        });
    }

    // Regex-based rewrite of literal ES module specifiers (dynamic import(), static import...from,
    // export...from, and bare side-effect import) in a standalone JavaScript response -- the
    // iteration-8 counterpart to RewriteCss, closing the gap iteration 7's runtime shim explicitly
    // couldn't (no browser hook exists to intercept native ESM import()). Resolves each specifier
    // against scriptUrl (the script's OWN url, not the page url -- mirrors RewriteCss resolving
    // against stylesheetUrl) since that's what the browser's module loader would do. Same UTF-8
    // assumption as RewriteCss for the same reason (PageDownloader doesn't expose charset). This is a
    // pragmatic text-level regex rewrite, not a real JS parser -- same accepted tier of imprecision as
    // RewriteCss: it cannot catch a specifier built from string concatenation/template literals or a
    // runtime chunk-manifest lookup (e.g. webpack's `__webpack_require__.p + id + ".js"`), and could
    // in principle mis-match `import`/`from` text sitting inside a comment or unrelated string -- both
    // are documented, out-of-scope gaps per plans/8_iteration_js_module_rewrite.md, not bugs to chase.
    // CommonJS require(...) is explicitly out of scope too (see that plan): bundlers targeting
    // CommonJS output typically use classic script-tag/JSONP chunk loading instead, which iteration
    // 7's runtime shim already covers.
    public byte[] RewriteJavaScript(byte[] jsBytes, Uri scriptUrl)
    {
        Uri serverOrigin = GetServerOrigin();
        string js = Encoding.UTF8.GetString(jsBytes);
        string rewritten = RewriteJavaScriptText(js, scriptUrl, serverOrigin);
        return Encoding.UTF8.GetBytes(rewritten);
    }

    // Shared regex-replace body for RewriteJavaScript. Order is dynamic-import, then bare-import, then
    // from-clause -- unlike RewriteCssText's two passes, order doesn't matter for correctness here
    // (the three regexes are mutually exclusive on any given "import"/"from" occurrence, see the
    // fields' comment above), but running dynamic/bare import first means a from-clause match can
    // never accidentally re-scan text this method itself just wrote for an import(...) call.
    private static string RewriteJavaScriptText(string js, Uri scriptUrl, Uri serverOrigin)
    {
        string result = DynamicImportRegex.Replace(js, match =>
        {
            string raw = match.Groups["url"].Value.Trim();
            return TryResolveNavigableUrl(raw, scriptUrl, out Uri? absolute)
                ? $"import(\"{BuildFetchUrl(absolute!, serverOrigin)}\")"
                : match.Value;
        });

        result = BareImportRegex.Replace(result, match =>
        {
            string raw = match.Groups["url"].Value.Trim();
            return TryResolveNavigableUrl(raw, scriptUrl, out Uri? absolute)
                ? $"import\"{BuildFetchUrl(absolute!, serverOrigin)}\""
                : match.Value;
        });

        result = FromClauseRegex.Replace(result, match =>
        {
            string raw = match.Groups["url"].Value.Trim();
            return TryResolveNavigableUrl(raw, scriptUrl, out Uri? absolute)
                ? $"from\"{BuildFetchUrl(absolute!, serverOrigin)}\""
                : match.Value;
        });

        return result;
    }

    // Resolves and rewrites a single element's URL-bearing attribute in place; leaves it untouched
    // if the value isn't a real navigable URL (see TryResolveNavigableUrl).
    private static void RewriteUrlAttribute(IElement element, string attributeName, Uri pageUrl, Uri serverOrigin)
    {
        string? raw = element.GetAttribute(attributeName);
        if (string.IsNullOrWhiteSpace(raw))
        {
            return;
        }

        if (TryResolveNavigableUrl(raw, pageUrl, out Uri? absolute))
        {
            element.SetAttribute(attributeName, BuildFetchUrl(absolute!, serverOrigin));
        }
    }

    // Filters out values that aren't real cross-origin-fetchable URLs before we bother resolving
    // them: fragment-only (#foo), javascript:/data:/mailto:/tel: pseudo-schemes, and anything that
    // doesn't parse or doesn't end up http(s) after being resolved against the page's base URL.
    private static bool TryResolveNavigableUrl(string value, Uri baseUrl, out Uri? absolute)
    {
        absolute = null;
        string trimmed = value.Trim();
        if (trimmed.Length == 0
            || trimmed.StartsWith('#')
            || SkipUrlPrefixes.Any(prefix => trimmed.StartsWith(prefix, StringComparison.OrdinalIgnoreCase)))
        {
            return false;
        }

        if (!Uri.TryCreate(baseUrl, trimmed, out Uri? resolved)
            || (resolved.Scheme != Uri.UriSchemeHttp && resolved.Scheme != Uri.UriSchemeHttps))
        {
            return false;
        }

        absolute = resolved;
        return true;
    }

    // Every rewritten reference points back at the same policy-gated fetch endpoint on OUR OWN
    // origin -- see this class's doc comment on why that boundary is intentional, and RewriteHtml's
    // <base>-tag comment on why this must be a fully-qualified absolute URL (scheme+host+port), not
    // a root-relative "/api/session/fetch?..." path, once a <base href> pointing at the target site
    // is present in the document.
    private static string BuildFetchUrl(Uri absolute, Uri serverOrigin) =>
        $"{serverOrigin.Scheme}://{serverOrigin.Authority}{FetchRelayPath}" + Uri.EscapeDataString(absolute.ToString());

    // Reads the current request's own scheme+host(:port) so rewritten URLs can be built as absolute
    // references back to this server (see BuildFetchUrl). IHttpContextAccessor rather than adding an
    // extra parameter to IContentRewriter keeps the interface matching the plan's exact shape while
    // still letting the value reflect the actual inbound Host header per request (works for both
    // dev's fixed localhost:5139 and a real deployment behind a different host/port, unlike a
    // hardcoded config value).
    private Uri GetServerOrigin()
    {
        HttpRequest? request = httpContextAccessor.HttpContext?.Request;
        if (request is null)
        {
            throw new InvalidOperationException("ContentRewriter was called outside of an active HTTP request; cannot determine the server's own origin.");
        }

        return new Uri($"{request.Scheme}://{request.Host}");
    }

    // Forces the document's declared charset to match the UTF-8 bytes RewriteHtml always serializes,
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
