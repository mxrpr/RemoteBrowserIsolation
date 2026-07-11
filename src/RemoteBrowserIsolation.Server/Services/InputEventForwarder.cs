using System.Diagnostics;
using System.Text.Json;
using System.Threading.Channels;
using Microsoft.Playwright;
using RemoteBrowserIsolation.Server.Models;
using SIPSorcery.Net;

namespace RemoteBrowserIsolation.Server.Services;

public interface IInputEventForwarder
{
    // allowKeyboard: false drops keydown/keyup before replay (VideoNoInput) -- mouse events (move,
    // down, up, wheel) are always replayed regardless.
    void Wire(RTCDataChannel inputChannel, IPage page, Uri targetUrl, bool allowKeyboard = true);
}

// Decodes JSON InputEvent messages arriving on the input data channel and replays them against the
// server-side rendered Page via Playwright's virtual mouse/keyboard, so client-side interaction
// (click, scroll, type) actually drives the isolated remote page instead of anything on the client.
public sealed class InputEventForwarder(ILogger<InputEventForwarder> logger) : IInputEventForwarder
{
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web);

    // Registers the data channel's onmessage handler. Events are pushed into an unbounded queue and
    // replayed by a single consumer loop: input order MUST be preserved (a mouseup replayed before
    // its mousedown is a lost click), so events cannot be dispatched fire-and-forget in parallel.
    // The queue also keeps SIPSorcery's receive thread unblocked while Playwright replays.
    public void Wire(RTCDataChannel inputChannel, IPage page, Uri targetUrl, bool allowKeyboard = true)
    {
        var queue = Channel.CreateUnbounded<byte[]>(new UnboundedChannelOptions { SingleReader = true });

        inputChannel.onmessage += (_, _, data) =>
        {
            if (!queue.Writer.TryWrite(data))
            {
                logger.LogWarning("Dropped input event for {Url}: queue closed", targetUrl);
            }
        };
        inputChannel.onclose += () => queue.Writer.TryComplete();

        _ = ReplayLoopAsync(queue.Reader, page, targetUrl, allowKeyboard);
    }

    // Single consumer: replays queued events strictly in arrival order until the channel closes.
    private async Task ReplayLoopAsync(ChannelReader<byte[]> reader, IPage page, Uri targetUrl, bool allowKeyboard)
    {
        await foreach (var data in reader.ReadAllAsync())
        {
            await HandleAsync(page, data, targetUrl, allowKeyboard);
        }
    }

    // Deserializes one input event and replays it on the page via the matching Playwright API.
    // allowKeyboard false drops keydown/keyup here -- server-authoritative, independent of whether
    // the client actually sends them.
    private async Task HandleAsync(IPage page, byte[] data, Uri targetUrl, bool allowKeyboard)
    {
        var receivedAt = Stopwatch.GetTimestamp();
        try
        {
            var inputEvent = JsonSerializer.Deserialize<InputEvent>(data, JsonOptions);
            if (inputEvent is null)
            {
                return;
            }

            var dispatchStart = Stopwatch.GetTimestamp();
            switch (inputEvent.Type)
            {
                case "mousemove":
                    await page.Mouse.MoveAsync(inputEvent.X ?? 0, inputEvent.Y ?? 0);
                    break;
                // Move to the event's own coordinates before pressing/releasing: DownAsync/UpAsync
                // act at the virtual mouse's current position, which can lag the real cursor since
                // the client coalesces mousemove events. The down/up pair itself constitutes the
                // click — the client deliberately does not send a separate "click" event (that
                // would make Playwright synthesize a second full click on top of this one).
                case "mousedown":
                    await page.Mouse.MoveAsync(inputEvent.X ?? 0, inputEvent.Y ?? 0);
                    await page.Mouse.DownAsync();
                    break;
                case "mouseup":
                    await page.Mouse.MoveAsync(inputEvent.X ?? 0, inputEvent.Y ?? 0);
                    await page.Mouse.UpAsync();
                    break;
                case "wheel":
                    await page.Mouse.WheelAsync(inputEvent.DeltaX ?? 0, inputEvent.DeltaY ?? 0);
                    break;
                case "keydown" when inputEvent.Key is not null && allowKeyboard:
                    await page.Keyboard.DownAsync(inputEvent.Key);
                    break;
                case "keyup" when inputEvent.Key is not null && allowKeyboard:
                    await page.Keyboard.UpAsync(inputEvent.Key);
                    break;
                case "keydown" or "keyup" when !allowKeyboard:
                    // VideoNoInput: dropped, not replayed.
                    break;
                default:
                    logger.LogWarning("Unhandled input event type {Type} for {Url}", inputEvent.Type, targetUrl);
                    break;
            }

            logger.LogInformation(
                "Replayed {Type} for {Url}: dispatch {DispatchMs:F1}ms, total {TotalMs:F1}ms",
                inputEvent.Type, targetUrl, Stopwatch.GetElapsedTime(dispatchStart).TotalMilliseconds, Stopwatch.GetElapsedTime(receivedAt).TotalMilliseconds);
        }
        catch (Exception ex)
        {
            logger.LogWarning(ex, "Failed to process input event for {Url}", targetUrl);
        }
    }
}
