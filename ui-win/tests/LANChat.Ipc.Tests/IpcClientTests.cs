using LANChat.Models;
using Xunit;

namespace LANChat.Ipc.Tests;

public sealed class IpcClientTests
{
    private static async Task<IpcClient> ConnectAsync(FakeRpcPipeServer server)
    {
        var client = new IpcClient(server.PipePath);
        await client.StartAsync();

        var deadline = DateTime.UtcNow.AddSeconds(5);
        while (!client.IsConnected && DateTime.UtcNow < deadline)
        {
            await Task.Delay(20);
        }

        if (!client.IsConnected)
        {
            throw new TimeoutException("测试客户端未能连接假服务端");
        }

        return client;
    }

    [Fact]
    public async Task InvokeAsync_returns_deserialized_state()
    {
        await using var server = new FakeRpcPipeServer();
        await using var client = await ConnectAsync(server);

        var state = await client.InvokeAsync<State>("GetState");

        Assert.Equal(ConnState.Connected, state.Conn);
        Assert.Equal("测试用户", state.Nickname);
        Assert.Single(state.Peers);
        Assert.Equal("张三", state.Peers[0].Nickname);
    }

    [Fact]
    public async Task InvokeAsync_throws_ipc_exception_on_error()
    {
        await using var server = new FakeRpcPipeServer();
        await using var client = await ConnectAsync(server);

        var ex = await Assert.ThrowsAsync<IpcException>(
            () => client.InvokeAsync<object>("FailMe"));
        Assert.Equal(-32603, ex.Code);
        Assert.Equal("模拟失败", ex.Message);
    }

    [Fact]
    public async Task Notifications_are_delivered_without_id()
    {
        await using var server = new FakeRpcPipeServer();
        await using var client = await ConnectAsync(server);

        // 触发服务端在响应后下发一条通知（见 FakeRpcPipeServer.NotificationToPush）
        var tcs = new TaskCompletionSource<(string Method, string From)>();
        client.Notification += (method, parameters) =>
        {
            if (method == "msg.received")
            {
                tcs.TrySetResult((method, parameters.GetProperty("from").GetString()!));
            }
        };

        await client.InvokeAsync<object>("TriggerNotification");

        var result = await tcs.Task.WaitAsync(TimeSpan.FromSeconds(5));
        Assert.Equal("msg.received", result.Method);
        Assert.Equal("p1", result.From);
    }
}
