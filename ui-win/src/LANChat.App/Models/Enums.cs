namespace LANChat.Models;

/// <summary>与 daemon 的连接状态（schema ConnState）。</summary>
public enum ConnState
{
    Disconnected,
    Connecting,
    Connected,
    AuthFailed,
}

/// <summary>传输任务状态（schema TransferState）。</summary>
public enum TransferState
{
    Pending,
    Active,
    Paused,
    Done,
    Failed,
    Canceled,
}

/// <summary>传输方向（schema TransferDirection）。</summary>
public enum TransferDirection
{
    Inbound,
    Outbound,
}

/// <summary>传输路径类型（schema PathKind）。</summary>
public enum PathKind
{
    Unicast,
    Swarm,
    Channel,
}
