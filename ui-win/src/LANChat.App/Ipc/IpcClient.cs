using System.Collections.Concurrent;
using System.IO.Pipes;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using LANChat.Models;

namespace LANChat.Ipc;

/// <summary>
/// 命名管道 JSON-RPC 2.0 客户端（单例）。
/// 一行一帧（UTF-8，\n 分隔）；带 id 的请求收到 result/error，
/// 无 id 的消息作为 notification 上报。断线自动重连。
/// </summary>
public sealed class IpcClient : IAsyncDisposable
{
    private const string DefaultPipePath = @"\\.\pipe\lanchat";

    private readonly string _pipeName;
    private readonly int _connectTimeoutMs;
    private readonly TimeSpan _requestTimeout;

    private CancellationTokenSource? _cts;
    private NamedPipeClientStream? _pipe;
    private StreamWriter? _writer;

    private readonly ConcurrentDictionary<long, TaskCompletionSource<JsonElement>> _pending = new();
    private long _nextId;
    private int _connected;

    /// <summary>连接建立/断开时触发（参数：是否已连接）。</summary>
    public event Action<bool>? ConnectionStateChanged;

    /// <summary>收到 daemon 下行通知：method 名 + params（已脱离解析文档，可长期持有）。</summary>
    public event Action<string, JsonElement>? Notification;

    public bool IsConnected => Volatile.Read(ref _connected) == 1;

    public IpcClient(string pipePath = DefaultPipePath, int connectTimeoutMs = 5000)
    {
        _pipeName = ExtractPipeName(pipePath);
        _connectTimeoutMs = connectTimeoutMs;
        _requestTimeout = TimeSpan.FromSeconds(15);
    }

    /// <summary>启动后台连接/读循环（不阻塞）。</summary>
    public Task StartAsync()
    {
        _cts = new CancellationTokenSource();
        _ = Task.Run(() => RunLoopAsync(_cts.Token));
        return Task.CompletedTask;
    }

    private async Task RunLoopAsync(CancellationToken ct)
    {
        while (!ct.IsCancellationRequested)
        {
            NamedPipeClientStream? pipe = null;
            try
            {
                pipe = new NamedPipeClientStream(
                    ".", _pipeName, PipeDirection.InOut, PipeOptions.Asynchronous);
                await pipe.ConnectAsync(_connectTimeoutMs, ct).ConfigureAwait(false);
                pipe.ReadMode = PipeTransmissionMode.Byte;

                _pipe = pipe;
                _writer = new StreamWriter(pipe, new UTF8Encoding(false))
                {
                    AutoFlush = true,
                    NewLine = "\n",
                };

                SetConnected(true);

                using var reader = new StreamReader(pipe, new UTF8Encoding(false));
                string? line;
                while (!ct.IsCancellationRequested && (line = await reader.ReadLineAsync(ct).ConfigureAwait(false)) != null)
                {
                    Dispatch(line);
                }
            }
            catch (OperationCanceledException)
            {
                break;
            }
            catch
            {
                // 连接失败 / 读失败：退避后重连
            }
            finally
            {
                SetConnected(false);
                FailPending(new IpcException(-32000, "IPC 连接已断开"));
                _writer = null;
                _pipe = null;
                pipe?.Dispose();
            }

            try
            {
                await Task.Delay(1000, ct).ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                break;
            }
        }
    }

    private void Dispatch(string line)
    {
        JsonDocument doc;
        try
        {
            doc = JsonDocument.Parse(line);
        }
        catch (JsonException)
        {
            return;
        }

        using (doc)
        {
            var root = doc.RootElement;
            if (root.TryGetProperty("id", out var idEl) && idEl.ValueKind == JsonValueKind.Number)
            {
                var id = idEl.GetInt64();
                if (_pending.TryRemove(id, out var tcs))
                {
                    tcs.TrySetResult(root.Clone());
                }
            }
            else if (root.ValueKind == JsonValueKind.Object &&
                     root.TryGetProperty("method", out var methodEl) &&
                     methodEl.ValueKind == JsonValueKind.String)
            {
                var method = methodEl.GetString() ?? "";
                var paramsEl = root.TryGetProperty("params", out var p) ? p.Clone() : default;
                Notification?.Invoke(method, paramsEl);
            }
        }
    }

    /// <summary>发起 JSON-RPC 请求并等待 result。</summary>
    public async Task<TResult> InvokeAsync<TResult>(string method, object? parameters = null, CancellationToken cancellationToken = default)
    {
        var result = await InvokeAsync(method, parameters, cancellationToken).ConfigureAwait(false);
        return result.Deserialize<TResult>(JsonOptions)!;
    }

    /// <summary>发起 JSON-RPC 请求，返回原始 result 元素（result 为空对象时返回 ValueKind=Object）。</summary>
    public async Task<JsonElement> InvokeAsync(string method, object? parameters = null, CancellationToken cancellationToken = default)
    {
        var id = Interlocked.Increment(ref _nextId);
        var tcs = new TaskCompletionSource<JsonElement>(TaskCreationOptions.RunContinuationsAsynchronously);
        _pending[id] = tcs;

        try
        {
            var payload = new Dictionary<string, object?>
            {
                ["jsonrpc"] = "2.0",
                ["id"] = id,
                ["method"] = method,
            };
            if (parameters is not null)
            {
                payload["params"] = parameters;
            }

            var writer = _writer;
            if (writer is null)
            {
                throw new InvalidOperationException("IPC 未连接");
            }

            await writer.WriteLineAsync(JsonSerializer.Serialize(payload, JsonOptions)).ConfigureAwait(false);

            using var timeoutCts = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            timeoutCts.CancelAfter(_requestTimeout);
            try
            {
                var response = await tcs.Task.WaitAsync(timeoutCts.Token).ConfigureAwait(false);
                if (response.TryGetProperty("error", out var error))
                {
                    var code = error.TryGetProperty("code", out var c) ? c.GetInt32() : -32603;
                    var message = error.TryGetProperty("message", out var m) ? m.GetString() ?? "未知错误" : "未知错误";
                    throw new IpcException(code, message);
                }

                return response.GetProperty("result");
            }
            catch (OperationCanceledException) when (!cancellationToken.IsCancellationRequested)
            {
                throw new TimeoutException($"IPC 请求超时：{method}");
            }
        }
        finally
        {
            _pending.TryRemove(id, out _);
        }
    }

    private void FailPending(Exception exception)
    {
        while (!_pending.IsEmpty)
        {
            foreach (var key in _pending.Keys)
            {
                if (_pending.TryRemove(key, out var tcs))
                {
                    tcs.TrySetException(exception);
                }
            }
        }
    }

    private void SetConnected(bool connected)
    {
        var value = connected ? 1 : 0;
        if (Interlocked.Exchange(ref _connected, value) != value)
        {
            ConnectionStateChanged?.Invoke(connected);
        }
    }

    private static string ExtractPipeName(string path)
    {
        const string prefix = @"\\.\pipe\";
        return path.StartsWith(prefix, StringComparison.OrdinalIgnoreCase)
            ? path[prefix.Length..]
            : path;
    }

    internal static readonly JsonSerializerOptions JsonOptions = CreateJsonOptions();

    private static JsonSerializerOptions CreateJsonOptions()
    {
        var options = new JsonSerializerOptions
        {
            PropertyNamingPolicy = JsonNamingPolicy.CamelCase,
            DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
        };
        options.Converters.Add(new JsonStringEnumConverter(JsonNamingPolicy.CamelCase));
        return options;
    }

    public async ValueTask DisposeAsync()
    {
        _cts?.Cancel();
        FailPending(new IpcException(-32001, "IPC 客户端已释放"));
        if (_pipe is not null)
        {
            try
            {
                await _pipe.FlushAsync().ConfigureAwait(false);
            }
            catch
            {
                // 忽略关闭时的刷新错误
            }
        }
        _pipe?.Dispose();
        _writer = null;
        _cts?.Dispose();
        _cts = null;
    }
}
