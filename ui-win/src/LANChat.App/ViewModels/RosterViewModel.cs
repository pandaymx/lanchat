using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using LANChat.Models;
using LANChat.Services;

namespace LANChat.ViewModels;

/// <summary>联系人/在线表：peer.joined / peer.left 维护。</summary>
public sealed partial class RosterViewModel : ViewModelBase
{
    private readonly AppServices _services;
    private readonly UiThread _ui;

    [ObservableProperty]
    private Peer? _selectedPeer;

    public ObservableCollection<Peer> Peers { get; } = [];

    /// <summary>选中联系人变化时通知聊天区切换会话。</summary>
    public event Action<Peer?>? SelectedPeerChanged;

    public RosterViewModel(AppServices services, UiThread ui)
    {
        _services = services;
        _ui = ui;
    }

    public void HandleConnChanged(ConnState conn)
    {
        if (conn == ConnState.Disconnected || conn == ConnState.AuthFailed)
        {
            Peers.Clear();
            SelectedPeer = null;
        }
    }

    public void ReplacePeers(IEnumerable<Peer> peers)
    {
        Peers.Clear();
        foreach (var peer in peers)
        {
            Peers.Add(peer);
        }
    }

    public void AddPeer(Peer peer)
    {
        if (!Peers.Any(p => p.Id == peer.Id))
        {
            Peers.Add(peer);
        }
    }

    public void RemovePeer(string peerId)
    {
        var existing = Peers.FirstOrDefault(p => p.Id == peerId);
        if (existing is not null)
        {
            Peers.Remove(existing);
        }
        if (SelectedPeer?.Id == peerId)
        {
            SelectedPeer = null;
        }
    }

    /// <summary>按 id 查询联系人（用于消息显示名称解析）。</summary>
    public Peer? FindPeer(string peerId) => Peers.FirstOrDefault(p => p.Id == peerId);

    partial void OnSelectedPeerChanged(Peer? value)
    {
        SelectedPeerChanged?.Invoke(value);
    }
}
