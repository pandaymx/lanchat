using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using LANChat.Models;
using LANChat.Services;

namespace LANChat.ViewModels;

/// <summary>
/// 频道列表：channel.updated / GetState 维护，区分已加入与可加入频道。
/// 频道均为公开可见；加入由 ChannelJoin 完成。
/// </summary>
public sealed partial class ChannelsViewModel : ViewModelBase
{
    private readonly AppServices _services;
    private readonly UiThread _ui;

    [ObservableProperty]
    private ChannelItem? _selectedChannel;

    public ObservableCollection<ChannelItem> Channels { get; } = [];

    /// <summary>选中频道变化时通知聊天区切换到频道会话。</summary>
    public event Action<Channel?>? SelectedChannelChanged;

    public ChannelsViewModel(AppServices services, UiThread ui)
    {
        _services = services;
        _ui = ui;
    }

    public void HandleConnChanged(ConnState conn)
    {
        if (conn == ConnState.Disconnected || conn == ConnState.AuthFailed)
        {
            Channels.Clear();
            SelectedChannel = null;
        }
    }

    /// <summary>用 channel.updated / GetState 的全量频道快照重建列表。</summary>
    public void ReplaceChannels(IEnumerable<Channel> channels, string? selfId)
    {
        Channels.Clear();
        foreach (var channel in channels)
        {
            Channels.Add(new ChannelItem(channel, selfId));
        }

        // 选中项按新快照刷新（成员变化后“已加入”状态要更新）
        if (SelectedChannel is not null)
        {
            var refreshed = Channels.FirstOrDefault(c => c.Channel.Id == SelectedChannel.Channel.Id);
            SelectedChannel = refreshed;
        }
    }

    /// <summary>加入频道；成功后服务端会下发 channel.updated 刷新成员。</summary>
    public async Task JoinAsync(Channel channel)
    {
        await _services.Ipc.InvokeAsync("ChannelJoin", new
        {
            channelID = channel.Id,
        });
    }

    /// <summary>创建频道；创建者自动成为成员，服务端随后下发 channel.updated。</summary>
    public async Task<string> CreateAsync(string name)
    {
        var result = await _services.Ipc.InvokeAsync("ChannelCreate", new
        {
            name,
        });
        return result.TryGetProperty("channelID", out var id) ? id.GetString() ?? "" : "";
    }

    /// <summary>按 id 查询频道（消息显示名称解析）。</summary>
    public Channel? FindChannel(string channelId) =>
        Channels.FirstOrDefault(c => c.Channel.Id == channelId)?.Channel;

    partial void OnSelectedChannelChanged(ChannelItem? value)
    {
        SelectedChannelChanged?.Invoke(value?.Channel);
    }
}

/// <summary>频道列表项：包装频道快照与当前用户的成员状态。</summary>
public sealed record ChannelItem
{
    public Channel Channel { get; }
    public bool IsMember { get; }

    public ChannelItem(Channel channel, string? selfId)
    {
        Channel = channel;
        IsMember = !string.IsNullOrEmpty(selfId) && channel.Members.Contains(selfId);
    }
}
