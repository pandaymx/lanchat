namespace LANChat.Ipc;

/// <summary>daemon 返回 JSON-RPC error 时抛出的异常。</summary>
public sealed class IpcException : Exception
{
    public int Code { get; }

    public IpcException(int code, string message) : base(message)
    {
        Code = code;
    }
}
