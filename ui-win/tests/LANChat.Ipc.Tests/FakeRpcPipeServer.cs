using System.IO.Pipes;
using System.Text;
using System.Text.Json;

namespace LANChat.Ipc.Tests;

/// <summary>
/// 基于 NamedPipeServerStream 的假 JSON-RPC 服务端：
/// 按 method 回 result/error，也可主动下发通知。
/// </summary>
public sealed class FakeRpcPipeServer : IAsyncDisposable
{
    private readonly CancellationTokenSource _cts = new();

    public string PipeName { get; }
    public string PipePath => $@"\\.\pipe\{PipeName}";
    public Task Completion { get; }

    public FakeRpcPipeServer()
    {
        PipeName = "lanchat-test-" + Guid.NewGuid().ToString("N");
        Completion = Task.Run(() => ServeAsync(_cts.Token));
    }

    private async Task ServeAsync(CancellationToken ct)
    {
        await using var server = new NamedPipeServerStream(
            PipeName, PipeDirection.InOut, 1,
            PipeTransmissionMode.Byte, PipeOptions.Asynchronous);
        await server.WaitForConnectionAsync(ct);

        var reader = new StreamReader(server, new UTF8Encoding(false));
        var writer = new StreamWriter(server, new UTF8Encoding(false))
        {
            AutoFlush = true,
            NewLine = "\n",
        };

        string? line;
        while (!ct.IsCancellationRequested && (line = await reader.ReadLineAsync(ct)) != null)
        {
            using var doc = JsonDocument.Parse(line);
            var root = doc.RootElement;
            var id = root.GetProperty("id");
            var method = root.GetProperty("method").GetString();

            object response = method switch
            {
                "GetState" => new
                {
                    jsonrpc = "2.0",
                    id,
                    result = new
                    {
                        conn = "connected",
                        selfId = "self1",
                        nickname = "测试用户",
                        peers = new[]
                        {
                            new { id = "p1", nickname = "张三", os = "windows", status = "online" },
                        },
                        transfers = Array.Empty<object>(),
                        channels = new[]
                        {
                            new
                            {
                                id = "c1",
                                name = "公共频道",
                                ownerId = "p1",
                                members = new[] { "p1" },
                            },
                        },
                    },
                },
                "ChannelCreate" => new
                {
                    jsonrpc = "2.0",
                    id,
                    result = new { channelID = "c-new" },
                },
                "ChannelList" => new
                {
                    jsonrpc = "2.0",
                    id,
                    result = new[]
                    {
                        new { id = "c1", name = "公共频道", ownerId = "p1", members = new[] { "p1" } },
                    },
                },
                "FailMe" => new
                {
                    jsonrpc = "2.0",
                    id,
                    error = new { code = -32603, message = "模拟失败" },
                },
                _ => new
                {
                    jsonrpc = "2.0",
                    id,
                    result = new { },
                },
            };

            await writer.WriteLineAsync(JsonSerializer.Serialize(response));

            if (method == "TriggerNotification")
            {
                await PushNotificationAsync(writer, "msg.received", new
                {
                    from = "p1",
                    msgId = "m1",
                    type = "text",
                    text = "你好",
                });
            }
            else if (method == "TriggerChannelNotification")
            {
                await PushNotificationAsync(writer, "channel.updated", new
                {
                    channels = new[]
                    {
                        new
                        {
                            id = "c1",
                            name = "公共频道",
                            ownerId = "p1",
                            members = new[] { "p1", "self1" },
                        },
                        new
                        {
                            id = "c2",
                            name = "另一个频道",
                            ownerId = "p1",
                            members = new[] { "p1" },
                        },
                    },
                });
            }
            else if (method == "TriggerGroupNotification")
            {
                await PushNotificationAsync(writer, "msg.received", new
                {
                    from = "p1",
                    group = "c1",
                    msgId = "g1",
                    type = "text",
                    text = "频道里的消息",
                });
            }
        }
    }

    private static async Task PushNotificationAsync(StreamWriter writer, string method, object @params)
    {
        var notification = new
        {
            jsonrpc = "2.0",
            method,
            @params,
        };
        await writer.WriteLineAsync(JsonSerializer.Serialize(notification));
    }

    public async ValueTask DisposeAsync()
    {
        _cts.Cancel();
        try
        {
            await Completion;
        }
        catch
        {
            // 忽略取消导致的退出
        }
        _cts.Dispose();
    }
}
