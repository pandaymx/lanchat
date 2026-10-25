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

    private readonly List<Peer> _allPeers = [];

    [ObservableProperty]
    private Peer? _selectedPeer;

    [ObservableProperty]
    private string _searchQuery = "";

    public ObservableCollection<Peer> Peers { get; } = [];

    /// <summary>在线人数统计文案。</summary>
    public string OnlineCountText => $"在线好友 ({_allPeers.Count})";

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
            _allPeers.Clear();
            Peers.Clear();
            SelectedPeer = null;
            OnPropertyChanged(nameof(OnlineCountText));
        }
    }

    public void ReplacePeers(IEnumerable<Peer> peers)
    {
        _allPeers.Clear();
        _allPeers.AddRange(peers);
        RefreshFilteredPeers();
        OnPropertyChanged(nameof(OnlineCountText));
    }

    public void AddPeer(Peer peer)
    {
        if (!_allPeers.Any(p => p.Id == peer.Id))
        {
            _allPeers.Add(peer);
            RefreshFilteredPeers();
            OnPropertyChanged(nameof(OnlineCountText));
        }
    }

    public void RemovePeer(string peerId)
    {
        var existing = _allPeers.FirstOrDefault(p => p.Id == peerId);
        if (existing is not null)
        {
            _allPeers.Remove(existing);
            RefreshFilteredPeers();
            OnPropertyChanged(nameof(OnlineCountText));
        }
        if (SelectedPeer?.Id == peerId)
        {
            SelectedPeer = null;
        }
    }

    /// <summary>按 id 查询联系人（用于消息显示名称解析）。</summary>
    public Peer? FindPeer(string peerId) => _allPeers.FirstOrDefault(p => p.Id == peerId);

    partial void OnSearchQueryChanged(string value)
    {
        RefreshFilteredPeers();
    }

    private void RefreshFilteredPeers()
    {
        var query = SearchQuery.Trim();
        var filtered = string.IsNullOrEmpty(query)
            ? _allPeers
            : _allPeers.Where(p =>
                p.Nickname.Contains(query, StringComparison.OrdinalIgnoreCase) ||
                p.Os.Contains(query, StringComparison.OrdinalIgnoreCase) ||
                p.Id.Contains(query, StringComparison.OrdinalIgnoreCase)).ToList();

        Peers.Clear();
        foreach (var peer in filtered)
        {
            Peers.Add(peer);
        }
    }

    partial void OnSelectedPeerChanged(Peer? value)
    {
        SelectedPeerChanged?.Invoke(value);
    }
}
