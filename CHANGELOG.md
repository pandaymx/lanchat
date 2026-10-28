# Changelog

## [7.0.2](https://github.com/pandaymx/lanchat/compare/v7.0.1...v7.0.2) (2026-10-28)


### Bug Fixes

* **ui-ios:** align gomobile Swift method selectors for contract v1.5 ([d9fd1e9](https://github.com/pandaymx/lanchat/commit/d9fd1e9b5021267691009593ec8d336d81e82822))

## [7.0.1](https://github.com/pandaymx/lanchat/compare/v7.0.0...v7.0.1) (2026-10-28)


### Bug Fixes

* **appapi:** allow schemaVersion 1.5 in TestSchemaIsValidV1 ([14de158](https://github.com/pandaymx/lanchat/commit/14de158daa6f4074f4b769679e3c814c4052ff22))

## [7.0.0](https://github.com/pandaymx/lanchat/compare/v6.3.0...v7.0.0) (2026-10-28)


### ⚠ BREAKING CHANGES

* **appapi:** upgrade IPC contract schema to v1.5 with 42 explicit RPC methods across all 6 endpoints.

### Features

* **appapi:** upgrade contract to v1.5 with 42 structured RPC methods ([623f70c](https://github.com/pandaymx/lanchat/commit/623f70c0693460a4ffbafbc5aeeaf721aa67de16))

## [6.3.0](https://github.com/pandaymx/lanchat/compare/v6.2.0...v6.3.0) (2026-10-28)


### Features

* **group:** add rarest-first block scheduler and endgame tail acceleration ([313f33c](https://github.com/pandaymx/lanchat/commit/313f33c99b73618e9ab7e4dc175e935306cfc46a))

## [6.2.0](https://github.com/pandaymx/lanchat/compare/v6.1.0...v6.2.0) (2026-10-28)


### Features

* **core:** add screen share laser dot overlay and desktop history/edit UI ([da577ad](https://github.com/pandaymx/lanchat/commit/da577ada1084e1d298c1661c710173f1c699cc46))

## [6.1.0](https://github.com/pandaymx/lanchat/compare/v6.0.1...v6.1.0) (2026-10-28)


### Features

* **ui-android:** add mobile v1.4 message edit, history pagination, and voice mute UI ([59cef95](https://github.com/pandaymx/lanchat/commit/59cef95699d00a58e595862d627b42f9472732d1))

## [6.0.1](https://github.com/pandaymx/lanchat/compare/v6.0.0...v6.0.1) (2026-10-28)


### Bug Fixes

* **ui-win:** resolve ChatViewModel helper references and SyncFolderAsync ([d4276e7](https://github.com/pandaymx/lanchat/commit/d4276e77fe86b1e04acb3aa0550912d4aa44fe71))

## [6.0.0](https://github.com/pandaymx/lanchat/compare/v5.3.0...v6.0.0) (2026-10-28)


### ⚠ BREAKING CHANGES

* **appapi:** upgrade to v6.0.0 with IPC contract v1.4 (37 methods) adding explicit RPC methods for message interactions (RevokeMessage, ReactMessage, EditMessage, PinMessage, MarkMessageRead) and session security controls (SetSessionE2EE, SetAutoClipboard, SetDND, GetHistoryPage)

### Features

* **appapi:** upgrade contract to v1.4 with structured message RPCs ([ca6d384](https://github.com/pandaymx/lanchat/commit/ca6d3841d678c5dff099028f8e805a3a31f18dd7))


### Bug Fixes

* **core:** remove unused packDirectoryToZip helper ([6c12cc2](https://github.com/pandaymx/lanchat/commit/6c12cc29c11e282a82bb7d08da08a177ce50ace6))

## [5.3.0](https://github.com/pandaymx/lanchat/compare/v5.2.0...v5.3.0) (2026-10-28)


### Features

* **core:** add cross-platform audio pipeline, jitter buffer, and UDP relay fallback ([9c4f5cb](https://github.com/pandaymx/lanchat/commit/9c4f5cbefb9c5cf4669b4f7d9c847847d881a560))

## [5.2.0](https://github.com/pandaymx/lanchat/compare/v5.1.0...v5.2.0) (2026-10-28)


### Features

* **core:** add streaming tar.gz directory transfer and safe auto-unpack ([0a75907](https://github.com/pandaymx/lanchat/commit/0a759071d6cb3ce154ceb4b09e079a1f969ce4c3))

## [5.1.0](https://github.com/pandaymx/lanchat/compare/v5.0.0...v5.1.0) (2026-10-28)


### Features

* **ui-android:** add mobile collab stage, call banner, and dual-stack badge ([a9457d8](https://github.com/pandaymx/lanchat/commit/a9457d8ef55e2eddf354bf083b3fb8ea046e0501))

## [5.0.0](https://github.com/pandaymx/lanchat/compare/v4.9.0...v5.0.0) (2026-10-27)


### ⚠ BREAKING CHANGES

* **appapi:** upgrade to v5.0.0 with IPC contract v1.3 explicit RPC methods for voice calls, screen sharing, annotations, folder sync, zero-trust node certificates, and network diagnostics

### Features

* **appapi:** upgrade contract to v1.3 with structured collab RPC methods ([af035f4](https://github.com/pandaymx/lanchat/commit/af035f453a9059f526cbea1588dceff899ebef1e))

## [4.9.0](https://github.com/pandaymx/lanchat/compare/v4.8.0...v4.9.0) (2026-10-27)


### Features

* **ui-linux:** add incoming call banner, native desktop notifications, and background residency ([ccfb8f6](https://github.com/pandaymx/lanchat/commit/ccfb8f61a26c9518a47c9877f0de314ebe49d755))

## [4.8.0](https://github.com/pandaymx/lanchat/compare/v4.7.0...v4.8.0) (2026-10-27)


### Features

* **core:** add real screen capture streaming pipeline and auto-clipboard sync ([1c5da0f](https://github.com/pandaymx/lanchat/commit/1c5da0f0c5d6d2e5e7cd1df14b63d0a209299960))

## [4.7.0](https://github.com/pandaymx/lanchat/compare/v4.6.1...v4.7.0) (2026-10-27)


### Features

* **discover:** add Tailscale zero-config peer discovery and dual-stack priority ([6a74677](https://github.com/pandaymx/lanchat/commit/6a74677481f7db1e2af4fbfb375e6968b0b62cd4))

## [4.6.1](https://github.com/pandaymx/lanchat/compare/v4.6.0...v4.6.1) (2026-10-27)


### Bug Fixes

* **discover:** add UDP broadcast beacon and auto-standby embedded hub ([2803aec](https://github.com/pandaymx/lanchat/commit/2803aec290243277c078349153bfc7fd931925dd))

## [4.6.0](https://github.com/pandaymx/lanchat/compare/v4.5.1...v4.6.0) (2026-10-27)


### Features

* **ui-win:** add Windows ARM64 build, MSI/MSIX packaging, and auto-update support ([ef2b73a](https://github.com/pandaymx/lanchat/commit/ef2b73a92dc64bf5344eaff04bb0d45140b9e8c6))

## [4.5.1](https://github.com/pandaymx/lanchat/compare/v4.5.0...v4.5.1) (2026-10-27)


### Bug Fixes

* **ui-linux:** eliminate GTK4 main-thread lag with lazy popovers, incremental rendering, and texture caching ([b966dec](https://github.com/pandaymx/lanchat/commit/b966dec12203a718c6cd0cc4b1dca6264beb8f76))

## [4.5.0](https://github.com/pandaymx/lanchat/compare/v4.4.0...v4.5.0) (2026-10-27)


### Features

* **ui-linux:** add native PipeWire/ALSA voice recording, playback, and UDP audio call bridge ([f272157](https://github.com/pandaymx/lanchat/commit/f2721576eaa2ce4293fcec4d9b9f269a369698bb))

## [4.4.0](https://github.com/pandaymx/lanchat/compare/v4.3.0...v4.4.0) (2026-10-27)


### Features

* **ui-ios:** upgrade Android and iOS clients to Mobile QQ 9.x collab stage and contract v1.2 ([edd388e](https://github.com/pandaymx/lanchat/commit/edd388ea9d5daef9e36cd0cfc2676575ec359231))

## [4.3.0](https://github.com/pandaymx/lanchat/compare/v4.2.0...v4.3.0) (2026-10-27)


### Features

* **ui-macos:** upgrade macOS SwiftUI client to NTQQ 9.x collab stage and contract v1.2 ([d9f997d](https://github.com/pandaymx/lanchat/commit/d9f997da5a5ac47ee04dccd8da78f11cd554f0b3))

## [4.2.0](https://github.com/pandaymx/lanchat/compare/v4.1.0...v4.2.0) (2026-10-27)


### Features

* **ui-linux:** upgrade Linux GTK4 client to NTQQ 9.x collab stage and contract v1.2 ([353e7cb](https://github.com/pandaymx/lanchat/commit/353e7cb87975f031568f88eed991af4e7b572e27))

## [4.1.0](https://github.com/pandaymx/lanchat/compare/v4.0.0...v4.1.0) (2026-10-27)


### Features

* **ui-win:** upgrade Windows client to NTQQ 9.x collab stage and contract v1.2 ([0791318](https://github.com/pandaymx/lanchat/commit/0791318175c6e208f3b4cecff0aad3b4ce1ee97e))

## [4.0.0](https://github.com/pandaymx/lanchat/compare/v3.4.0...v4.0.0) (2026-10-27)


### ⚠ BREAKING CHANGES

* **core:** upgrade to v4.0.0 with Ed25519 zero-trust node admission certificates and LAN diagnostics radar

### Features

* **core:** add Ed25519 zero-trust node certificates and LAN link diagnostics ([f37e3c8](https://github.com/pandaymx/lanchat/commit/f37e3c8f24001bbf3cd0634827ac6e540383af41))

## [3.4.0](https://github.com/pandaymx/lanchat/compare/v3.3.0...v3.4.0) (2026-10-27)


### Features

* **core:** add multi-dimensional encrypted FTS and P2P folder incremental sync ([356f45d](https://github.com/pandaymx/lanchat/commit/356f45d6e9f98db624bb0ea648d7a84722f0acdd))

## [3.3.0](https://github.com/pandaymx/lanchat/compare/v3.2.0...v3.3.0) (2026-10-27)


### Features

* **core:** add screen share annotation, auto-clipboard sync, and code block cards ([8fc3588](https://github.com/pandaymx/lanchat/commit/8fc3588e16ff009159a4108faccc6da3ef69d705))

## [3.2.0](https://github.com/pandaymx/lanchat/compare/v3.1.0...v3.2.0) (2026-10-27)


### Features

* **ui-linux:** align screen share and history roaming sync controls across Linux and iOS ([84f977a](https://github.com/pandaymx/lanchat/commit/84f977a860012b12998b07f0b7168d362b181a93))

## [3.1.0](https://github.com/pandaymx/lanchat/compare/v3.0.1...v3.1.0) (2026-10-27)


### Features

* **appapi:** evolve contract to v1.2 with voice call and UDP screen share stream ([705044e](https://github.com/pandaymx/lanchat/commit/705044e041c4066c342ae70925189f8ff8c9c45a))

## [3.0.1](https://github.com/pandaymx/lanchat/compare/v3.0.0...v3.0.1) (2026-10-27)


### Bug Fixes

* **ci:** resolve golangci-lint unused helper and macOS async call errors ([17006e9](https://github.com/pandaymx/lanchat/commit/17006e959e1dff2758ede377c8394950649726ca))

## [3.0.0](https://github.com/pandaymx/lanchat/compare/v2.4.0...v3.0.0) (2026-10-26)


### ⚠ BREAKING CHANGES

* **core:** add mesh hub automatic failover and multi-device history roaming sync (v3.0.0)

### Features

* **core:** add mesh hub automatic failover and multi-device history roaming sync (v3.0.0) ([a5a9845](https://github.com/pandaymx/lanchat/commit/a5a98456980d6654e557c2d115036089a46694db))

## [2.4.0](https://github.com/pandaymx/lanchat/compare/v2.3.0...v2.4.0) (2026-10-26)


### Features

* **core:** add multi-photo album P2P batch transfer and lanchat://join room invite URI (v2.4.0) ([d82bee1](https://github.com/pandaymx/lanchat/commit/d82bee1472c48ba9e4b128923ddbf8a739879c09))

## [2.3.0](https://github.com/pandaymx/lanchat/compare/v2.2.0...v2.3.0) (2026-10-26)


### Features

* **core:** add P2P screen share snap encoding and native live canvas UI (v2.3.0) ([fbbc991](https://github.com/pandaymx/lanchat/commit/fbbc991bb4296334912eedb526d74bba5f6c6bbd))

## [2.2.0](https://github.com/pandaymx/lanchat/compare/v2.1.0...v2.2.0) (2026-10-26)


### Features

* **ui-android:** add one-tap embedded LAN hub mode and FileProvider auto-install on silent update (v2.2.0) ([72f2f51](https://github.com/pandaymx/lanchat/commit/72f2f51c7291ffd185aa63de5b3f6cfc7d5ab648))

## [2.1.0](https://github.com/pandaymx/lanchat/compare/v2.0.1...v2.1.0) (2026-10-26)


### Features

* **bindings:** expose v2.0 voice/screen/history APIs to mobile and add 5-client voice call UI (v2.1.0) ([9e0728b](https://github.com/pandaymx/lanchat/commit/9e0728bb96da137db9f2dcd9a11f59eadb3b41ae))

## [2.0.1](https://github.com/pandaymx/lanchat/compare/v2.0.0...v2.0.1) (2026-10-26)


### Bug Fixes

* **ui-android:** handle system back gesture in ChatScreen and MainScreen to return home ([dca4785](https://github.com/pandaymx/lanchat/commit/dca47850bbaafab344220e37b5c89a8d88d0a304))

## [2.0.0](https://github.com/pandaymx/lanchat/compare/v1.19.0...v2.0.0) (2026-10-26)


### ⚠ BREAKING CHANGES

* **core:** promote LANChat to v2.0.0 with IPC v1.1 structured messages, CAS media storage, and real-time LAN voice/screen/overlay collaboration

### Features

* **core:** add LAN UDP voice calls, P2P screen share, and overlay gossip (v2.0.0) ([7d39124](https://github.com/pandaymx/lanchat/commit/7d391246c2571550e6890990be30fc84442b2e22))
* **discover:** add cross-VLAN/Tailscale overlay probing and GossipPeerBook ([61ea9b4](https://github.com/pandaymx/lanchat/commit/61ea9b4ab089c51b8381205dd84c9a1fe9e2b727))

## [1.19.0](https://github.com/pandaymx/lanchat/compare/v1.18.0...v1.19.0) (2026-10-26)


### Features

* **core:** add JSONL auto-compaction, cursor pagination, and CAS media externalization (v1.19.0) ([a8c6e70](https://github.com/pandaymx/lanchat/commit/a8c6e708b6c0d4bb3540b9ac7d7b93b5fadb464b))

## [1.18.0](https://github.com/pandaymx/lanchat/compare/v1.17.4...v1.18.0) (2026-10-26)


### Features

* **appapi:** evolve IPC schema v1.1 with structured ChatMessage fields and msg.updated ([62489d0](https://github.com/pandaymx/lanchat/commit/62489d0c7c592f999718ff1fed91479f9ccc46ee))
* **core:** populate structured ChatMessage fields and emit msg.updated (v1.18.0) ([a2d04ce](https://github.com/pandaymx/lanchat/commit/a2d04ce2f374e62a3870877dfe13e5e38a66f5a4))
* **ipc:** broadcast msg.updated notifications from MessageUpdateListener ([58c8f35](https://github.com/pandaymx/lanchat/commit/58c8f35fdc7f4f453808823a1f337572c9905a5b))
* **ui-android:** sync IPC v1.1 structured ChatMessage fields and bump to 1.18.0 ([646bb0b](https://github.com/pandaymx/lanchat/commit/646bb0ba83f622a29825278adcfed5f66ed26647))
* **ui-ios:** sync IPC v1.1 structured HistoryMessageJSON fields ([4cb44d2](https://github.com/pandaymx/lanchat/commit/4cb44d26c6b8d706e6d6805acd7affd29a65c284))
* **ui-linux:** sync IPC v1.1 structured HistoryMessage fields and msg.updated ([3b35711](https://github.com/pandaymx/lanchat/commit/3b35711e2ba85dbbcce915e24a99c9a1df474d4f))
* **ui-macos:** sync IPC v1.1 structured HistoryMessage fields and msg.updated ([96e5253](https://github.com/pandaymx/lanchat/commit/96e525393c8b8486e46325e57f28b719ca08d33c))
* **ui-win:** sync IPC v1.1 structured ChatMessage fields and msg.updated ([a6f86d4](https://github.com/pandaymx/lanchat/commit/a6f86d404f0921dc0c3fc6569b6099b3f96b4e52))

## [1.17.4](https://github.com/pandaymx/lanchat/compare/v1.17.3...v1.17.4) (2026-10-26)


### Bug Fixes

* **ui-android:** support real voice recording, playback, and home UI polish ([b2e76fc](https://github.com/pandaymx/lanchat/commit/b2e76fce85b4def3effd04651777f6e9cd7c1a56))

## [1.17.3](https://github.com/pandaymx/lanchat/compare/v1.17.2...v1.17.3) (2026-10-26)


### Bug Fixes

* **ui-android:** fix emoji surrogate avatar glyph and align chat bubble with avatar ([cf4fff3](https://github.com/pandaymx/lanchat/commit/cf4fff351f71f956fc98d5ed6645f931a7351b06))

## [1.17.2](https://github.com/pandaymx/lanchat/compare/v1.17.1...v1.17.2) (2026-10-26)


### Bug Fixes

* **server:** synchronize tuiEventListener model pointer access under race detector ([7eb58a4](https://github.com/pandaymx/lanchat/commit/7eb58a4ca414c517f2dcde8b44f983a98e6e2e70))
* **ui-android:** fix chat composer IME padding and dark mode cursor visibility ([45df902](https://github.com/pandaymx/lanchat/commit/45df9027a2563b79108b1931cc9d98de595a4f74))

## [1.17.1](https://github.com/pandaymx/lanchat/compare/v1.17.0...v1.17.1) (2026-10-26)


### Bug Fixes

* **core:** fetch channel list immediately on connect and bump version to 1.17.1 ([98b3e69](https://github.com/pandaymx/lanchat/commit/98b3e69702974b26f0496629fe722d7689f23784))
* **ui-android:** fix channel header button wrapping and state sync on 360dp screens ([9ab4176](https://github.com/pandaymx/lanchat/commit/9ab41768e5d493e9423488a0cda9a11712189e7f))

## [1.17.0](https://github.com/pandaymx/lanchat/compare/v1.16.0...v1.17.0) (2026-10-26)


### Features

* **core:** add M32 DND mode, unread/mention tracking, and M33 /update apply ([43055a1](https://github.com/pandaymx/lanchat/commit/43055a1e552d091fab8d1ef09ebaa0f9a5769339))
* **server:** display DND and unread/mention badges in interactive TUI header ([f59cb0d](https://github.com/pandaymx/lanchat/commit/f59cb0d3f2a98e708696af691d505027a6184535))

## [1.16.0](https://github.com/pandaymx/lanchat/compare/v1.15.0...v1.16.0) (2026-10-26)


### Features

* **core:** add M31 serverless mDNS mesh split-brain convergence and /mesh command ([25e68cc](https://github.com/pandaymx/lanchat/commit/25e68cc6e0e3d7cb8bd9257115831bab0ae238e5))

## [1.15.0](https://github.com/pandaymx/lanchat/compare/v1.14.0...v1.15.0) (2026-10-26)


### Features

* **core:** add M30 burn-after-reading self-destruct messages and history purge ([7059312](https://github.com/pandaymx/lanchat/commit/7059312f486f4a0b394486823302a9ac50c37a83))
* **server:** support M30 MSG_BURN and CLIP_SYNC events in interactive TUI ([633d65c](https://github.com/pandaymx/lanchat/commit/633d65c29d6fcfa727e503c02362790e26ed21d4))
* **ui-android:** handle M30 MSG_BURN self-destruct purge and CLIP_SYNC notifications ([72c08e4](https://github.com/pandaymx/lanchat/commit/72c08e4132f2c1506b69b4baae8de27744ae03e9))
* **ui-ios:** handle M30 MSG_BURN self-destruct purge and CLIP_SYNC notifications ([5690f67](https://github.com/pandaymx/lanchat/commit/5690f67689857153890ec5005d3685f1addc8e05))
* **ui-linux:** handle M30 MSG_BURN self-destruct purge and CLIP_SYNC notifications ([7a7e09c](https://github.com/pandaymx/lanchat/commit/7a7e09c7418e1a17164d88febef21cbf982d00e4))
* **ui-macos:** handle M30 MSG_BURN self-destruct purge and CLIP_SYNC notifications ([d8758f6](https://github.com/pandaymx/lanchat/commit/d8758f6d8c1d718357da7ca23e51930a6ef205b0))
* **ui-win:** handle M30 MSG_BURN self-destruct purge and CLIP_SYNC notifications ([8593ce4](https://github.com/pandaymx/lanchat/commit/8593ce4fbf1d88f16057c291b010ca341fd8f708))

## [1.14.0](https://github.com/pandaymx/lanchat/compare/v1.13.0...v1.14.0) (2026-10-26)


### Features

* **core:** add LAN clipboard sync (/clip) and screenshot paste-to-send (/paste) (M29) ([edfa60e](https://github.com/pandaymx/lanchat/commit/edfa60e4f640410b5397abfc05df9467b0e876bf))
* **server:** display /clip sync notifications in TUI and CLI ([8be3d78](https://github.com/pandaymx/lanchat/commit/8be3d78dd1bd80da7acdc6dd82599069a7bb0e05))
* **ui-linux:** add clipboard toolbar button for /clip sync and image paste ([a9c4cdf](https://github.com/pandaymx/lanchat/commit/a9c4cdf9d0efdfec8bc8e3dae9c51d8fd0da7f54))
* **ui-macos:** add clipboard paste-to-send and /clip sync toolbar button ([ac7082a](https://github.com/pandaymx/lanchat/commit/ac7082a4beee9e774ea8b329be11c3d3205a183a))
* **ui-win:** add clipboard screenshot paste-to-send and /clip sync button ([c8730db](https://github.com/pandaymx/lanchat/commit/c8730dbedfb222c73a446850aff7a943b13be351))

## [1.13.0](https://github.com/pandaymx/lanchat/compare/v1.12.0...v1.13.0) (2026-10-26)


### Features

* **core:** stream directory tree bundles and auto-unpack on receive (M28) ([e588924](https://github.com/pandaymx/lanchat/commit/e588924568732417205394c06cc40d93b738225f))
* **server:** support /senddir and /folder commands in TUI client ([cee3c6e](https://github.com/pandaymx/lanchat/commit/cee3c6eb6e16b3cebfc5718dd0a767b8cb68acc5))
* **ui-linux:** add folder picker toolbar button for directory transfer ([d247040](https://github.com/pandaymx/lanchat/commit/d247040d226b2720f80719e4915a1862078c3ab9))
* **ui-macos:** support drag-and-drop file and folder transfer in chat view ([fb3db7c](https://github.com/pandaymx/lanchat/commit/fb3db7c190756aa640d00ec994f008d28a9dccbe))
* **ui-win:** add folder picker and drag-and-drop directory transfer ([7a6ba2a](https://github.com/pandaymx/lanchat/commit/7a6ba2a1eba32c82825b6fecdc56d216744149f0))

## [1.12.0](https://github.com/pandaymx/lanchat/compare/v1.11.1...v1.12.0) (2026-10-26)


### Features

* **core:** add atomic CLI binary self-update via domestic mirror fallback ([617c9aa](https://github.com/pandaymx/lanchat/commit/617c9aaa374cd96a3c13ab0c4234585844d83371))
* **server:** add full-screen interactive TUI client and update subcommand ([5e41554](https://github.com/pandaymx/lanchat/commit/5e41554998482f5fa0699e89e8665159c07b0ec5))

## [1.11.1](https://github.com/pandaymx/lanchat/compare/v1.11.0...v1.11.1) (2026-10-26)


### Bug Fixes

* **ci:** keep ELF dynamic symbols and 16KB alignment in Android gomobile AAR ([553dd80](https://github.com/pandaymx/lanchat/commit/553dd808eb3b64980ac10a3fea1e8e8f58d81bc8))
* **ui-android:** fix startup crash from missing multicast permission and service race ([89a88c3](https://github.com/pandaymx/lanchat/commit/89a88c395d7bc5599470ba024b10366043b43d03))

## [1.11.0](https://github.com/pandaymx/lanchat/compare/v1.10.4...v1.11.0) (2026-10-26)


### Features

* **ui-android:** redesign QQ chat bubbles, recall re-edit pill, and popup menu ([8de65d7](https://github.com/pandaymx/lanchat/commit/8de65d7d78ab2c9eb5fde247536e4be7d171dc29))
* **ui-ios:** redesign QQ chat bubbles, recall re-edit pill, and quote card ([6aaf342](https://github.com/pandaymx/lanchat/commit/6aaf342a347842d5604bc4b68b9d3d6b87c3d747))
* **ui-linux:** redesign QQ chat bubbles, recall re-edit pill, and popover menu ([782c070](https://github.com/pandaymx/lanchat/commit/782c07071686378fbc846b99fd3b393f62178e28))
* **ui-macos:** redesign QQ chat bubbles, recall re-edit pill, and quote card ([9c9d9fb](https://github.com/pandaymx/lanchat/commit/9c9d9fb159490a7b55c325bf356de29f4fa8cba4))
* **ui-win:** redesign QQ chat bubbles, recall re-edit pill, and context menu ([24334d1](https://github.com/pandaymx/lanchat/commit/24334d103430c116e9ca5611be4139605a2947f5))

## [1.10.4](https://github.com/pandaymx/lanchat/compare/v1.10.3...v1.10.4) (2026-10-26)


### Bug Fixes

* **core:** bump client version to 1.10.4 for Windows MSIX payload slimming ([f6c914a](https://github.com/pandaymx/lanchat/commit/f6c914a5c037f51704e62ad0c9e255c61cd8faf0))


### Performance Improvements

* **ui-win:** batch MSBuild payload pruning on ReferenceCopyLocalPaths and AppxPackagePayload ([4d67675](https://github.com/pandaymx/lanchat/commit/4d67675e1c6c963dcfb0ab3ee9326fff5bad3a34))

## [1.10.3](https://github.com/pandaymx/lanchat/compare/v1.10.2...v1.10.3) (2026-10-26)


### Bug Fixes

* **core:** bump client version to 1.10.3 for slimmed Windows and Android packages ([7941716](https://github.com/pandaymx/lanchat/commit/7941716376937f1095739d4aafccffc4d0deaee9))


### Performance Improvements

* **ci:** strip gomobile AAR symbols and enable high-compression MSI/ZIP packaging ([815e296](https://github.com/pandaymx/lanchat/commit/815e296de3c42e003fbe3ef02b0830411e58710a))
* **ui-android:** enable R8 resource shrinking, Deflate JNI packaging and arm64 ABI filter ([946e94f](https://github.com/pandaymx/lanchat/commit/946e94f445457f8ad570511ae41ca3d55473befb))
* **ui-win:** prune unreferenced BCL/debugger payloads and satellite locales from MSIX/MSI ([ac866b1](https://github.com/pandaymx/lanchat/commit/ac866b111eb533ddd245d9ef5704bb83d150174a))

## [1.10.2](https://github.com/pandaymx/lanchat/compare/v1.10.1...v1.10.2) (2026-10-26)


### Bug Fixes

* **core:** exclude diagnostic outputs from chat history and strip reactions in reply quotes ([f0490dc](https://github.com/pandaymx/lanchat/commit/f0490dc50fbd26dd5babc66fab290651b3c94d79))
* **ui-android:** show command diagnostics via snackbar instead of chat bubbles ([bd54d2c](https://github.com/pandaymx/lanchat/commit/bd54d2c4202809012baf0eaa4d14023edbea5f89))
* **ui-ios:** filter diagnostic pseudo-messages and sanitize quoted reply bubbles ([657b229](https://github.com/pandaymx/lanchat/commit/657b2298fe6a4c2ab794266ae23565bdcf22aeec))
* **ui-linux:** filter diagnostic pseudo-messages and sanitize quoted reply bubbles ([b029fac](https://github.com/pandaymx/lanchat/commit/b029facf92bf23a68936b8fbaafa99d9647b6500))
* **ui-macos:** filter diagnostic pseudo-messages and sanitize quoted reply bubbles ([d45866a](https://github.com/pandaymx/lanchat/commit/d45866a9ecb598db18b03bf993aafebb40841fbb))
* **ui-win:** filter diagnostic pseudo-messages and sanitize quoted reply bubbles ([5b77d4f](https://github.com/pandaymx/lanchat/commit/5b77d4fdded368a79f75266b1412231d71afd7cd))

## [1.10.1](https://github.com/pandaymx/lanchat/compare/v1.10.0...v1.10.1) (2026-10-26)


### Bug Fixes

* **ui-android:** insert selected emoji into input box instead of sending immediately ([af001a0](https://github.com/pandaymx/lanchat/commit/af001a0d3c7839f452e805312948d3bd0bf72e14))
* **ui-ios:** insert selected emoji into input box instead of sending immediately ([2d18433](https://github.com/pandaymx/lanchat/commit/2d184330108bc7759e461de0adde5d736178584e))
* **ui-linux:** insert selected emoji into input box instead of sending immediately ([42a408e](https://github.com/pandaymx/lanchat/commit/42a408ef280cf5fe045b63c5b6c928bfa049fa52))
* **ui-macos:** insert selected emoji into input box instead of sending immediately ([0a8ad0d](https://github.com/pandaymx/lanchat/commit/0a8ad0d845ddd2f6bf1372f60f134ac528d5348f))
* **ui-win:** close flyout and focus input box after inserting emoji ([5b8b155](https://github.com/pandaymx/lanchat/commit/5b8b15539cfd82f0278d6b6a6d96c00b7652ba52))

## [1.10.0](https://github.com/pandaymx/lanchat/compare/v1.9.3...v1.10.0) (2026-10-26)


### Features

* **core:** add silent update downloader with domestic proxy fallback ([a670cc2](https://github.com/pandaymx/lanchat/commit/a670cc21ab0302a57f4f5cf88b8f4291feaddb86))
* **ui-android:** add silent update action with domestic proxy fallback ([c9811e2](https://github.com/pandaymx/lanchat/commit/c9811e2dc11008f9766e205e5a74bf8af9855015))
* **ui-ios:** add silent update card with domestic proxy fallback ([9771a78](https://github.com/pandaymx/lanchat/commit/9771a78b52f3e4170a25d06fc33a863a3cec9336))
* **ui-linux:** add silent update dialog with domestic proxy fallback ([66896d5](https://github.com/pandaymx/lanchat/commit/66896d5254d452ed541616d5f7dd21f8e8ed8e5a))
* **ui-macos:** add silent update card with domestic proxy fallback ([8f897d4](https://github.com/pandaymx/lanchat/commit/8f897d43eea9caa5dd9eda583db171c2c1a8fed0))
* **ui-win:** add silent update card with domestic proxy support ([aefa344](https://github.com/pandaymx/lanchat/commit/aefa344d4ca9550055bbce144d470e7f480c6637))

## [1.9.3](https://github.com/pandaymx/lanchat/compare/v1.9.2...v1.9.3) (2026-10-26)


### Bug Fixes

* **core:** emit local reaction events and preserve reaction badges on edit ([b5782ea](https://github.com/pandaymx/lanchat/commit/b5782eab39aa6806638b2e8009f1877fcf8802cc))
* **ui-android:** render reaction pills beneath chat bubbles instead of inside text ([2801613](https://github.com/pandaymx/lanchat/commit/2801613863277fea01153555c7f17b7663aa6b51))
* **ui-ios:** render reaction pills and like button beneath chat bubbles ([a70a78f](https://github.com/pandaymx/lanchat/commit/a70a78fb590e8eec74ebbd1d5e741fec8f5f2b5d))
* **ui-linux:** render reaction pills and like action beneath chat bubbles ([ef4c429](https://github.com/pandaymx/lanchat/commit/ef4c42925ebdf0b19850af8f52acd3eb08d5413b))
* **ui-macos:** render reaction pills and like button beneath chat bubbles ([c8bbd8e](https://github.com/pandaymx/lanchat/commit/c8bbd8e61b6d86678eec8c7b523f9d8513a0f0e3))
* **ui-win:** render reaction badges and like button beneath chat bubbles ([2c473ee](https://github.com/pandaymx/lanchat/commit/2c473eef3f77f52dcd6f05691216b07c0666568b))

## [1.9.2](https://github.com/pandaymx/lanchat/compare/v1.9.1...v1.9.2) (2026-10-26)


### Bug Fixes

* **core:** terminate readPump on ServerShutdown to trigger failover ([20e80f2](https://github.com/pandaymx/lanchat/commit/20e80f252ed7f3de0aebc72e763ffa1e0cf0e2a1))

## [1.9.1](https://github.com/pandaymx/lanchat/compare/v1.9.0...v1.9.1) (2026-10-26)


### Bug Fixes

* **ci:** stop stale lanchat-daemon during Linux package upgrade ([216e88c](https://github.com/pandaymx/lanchat/commit/216e88c5e828b3a312551021a7dcb93711481104))
* **ipc:** close listener on shutdown and allow idempotent Connect ([7776427](https://github.com/pandaymx/lanchat/commit/7776427e3261f8071dd953dcef8be3982e51236c))
* **ui-linux:** make login error copyable and reap stale daemon on start ([0f4cc20](https://github.com/pandaymx/lanchat/commit/0f4cc20e0ae5abcc9a160afb053ca0b1822ab738))

## [1.9.0](https://github.com/pandaymx/lanchat/compare/v1.8.0...v1.9.0) (2026-10-26)


### Features

* **config:** add state-file server configuration option ([33119fe](https://github.com/pandaymx/lanchat/commit/33119fe848834db6811c3e7015bf55b111b25420))
* **core:** add encrypted history at rest, CAS instant transfer, and /status ([3a98218](https://github.com/pandaymx/lanchat/commit/3a98218d0cd050f251cdee4a2cc46e89204e310a))
* **server:** add Prometheus /metrics endpoint, FILE_DONE routing, and CLI client ([2070996](https://github.com/pandaymx/lanchat/commit/207099602d4ce50663025cccdbf093eeda2a7ee8))
* **transfer:** add HKDF and AES-GCM helpers for at-rest history encryption ([f6ef618](https://github.com/pandaymx/lanchat/commit/f6ef618772e25d61a897de948c8429d8786f2e6f))
* **ui-android:** support /status diagnostic command display ([66cef5c](https://github.com/pandaymx/lanchat/commit/66cef5c1d32b73354ef4db67c1ab5d9d64b2fefa))
* **ui-ios:** support /status diagnostic command display ([3a661aa](https://github.com/pandaymx/lanchat/commit/3a661aa4854681603eb9a51e6c7bb20fa7759d6b))
* **ui-linux:** support /status diagnostic command display ([504ed90](https://github.com/pandaymx/lanchat/commit/504ed903d621999ac3a467ba488f4c6a82df22e3))
* **ui-macos:** support /status diagnostic command display ([d0449cc](https://github.com/pandaymx/lanchat/commit/d0449ccd5cff4218bbfe5492aff947850e6d830e))
* **ui-win:** support /status diagnostic command display ([095d0d0](https://github.com/pandaymx/lanchat/commit/095d0d067331ff49bae72e9e654cea11a8d0382e))

## [1.8.0](https://github.com/pandaymx/lanchat/compare/v1.7.0...v1.8.0) (2026-10-25)


### Features

* **core:** add channel E2EE, auto-heal rejoin, reactions, edit, pin, and bwlimit ([02c240f](https://github.com/pandaymx/lanchat/commit/02c240f9849c480f25456d7b00e81933b82bf9a8))
* **group:** support restoring persisted channel registry state ([317244d](https://github.com/pandaymx/lanchat/commit/317244dcf17d9e942f5454528d1da53272c249ab))
* **server:** add atomic JSON persistence for offline spool and channel history ([72a27d6](https://github.com/pandaymx/lanchat/commit/72a27d6173aa8cced3c32bb6f7a3bdab335aca0c))
* **transfer:** add channel E2EE cipher and token-bucket bandwidth limiter ([25540ca](https://github.com/pandaymx/lanchat/commit/25540cad59661caf40feb9ff3b0eb803b4d3ab92))
* **ui-android:** support message reactions, edit, pin, and bandwidth limit ([bfe2eaf](https://github.com/pandaymx/lanchat/commit/bfe2eafb07d7ec8422df93315a03bb76fad48db1))
* **ui-ios:** support message reactions, edit, pin, and bandwidth limit ([61c99cb](https://github.com/pandaymx/lanchat/commit/61c99cb3beeb43693b1b0fae0641e3bd8e9f38e6))
* **ui-linux:** support message reactions, edit, pin, and bandwidth limit ([38f7e7e](https://github.com/pandaymx/lanchat/commit/38f7e7e33c1bbfd7e1cfffccbecec6ede4f8b980))
* **ui-macos:** support message reactions, edit, pin, and bandwidth limit ([5edae06](https://github.com/pandaymx/lanchat/commit/5edae06eb5dc27f24d53b49735e02d7890dac9c4))
* **ui-win:** support message reactions, edit, pin, and bandwidth limit ([e2bac40](https://github.com/pandaymx/lanchat/commit/e2bac4042be2cb13aecb08f4846ad132bd6bba65))

## [1.7.0](https://github.com/pandaymx/lanchat/compare/v1.6.1...v1.7.0) (2026-10-25)


### Features

* **core:** implement auto-failover, persistent X25519 E2EE, and inline media ([6951d58](https://github.com/pandaymx/lanchat/commit/6951d5872687334a68f6ee270649184e28d3e3b4))
* **protocol:** add X25519 identity pubKey fields for M15 E2EE ([698041f](https://github.com/pandaymx/lanchat/commit/698041feca90130a7024cc0f0e1d7a2a4cd89bea))
* **server:** add offline message spool and channel history replay ([d2f01a9](https://github.com/pandaymx/lanchat/commit/d2f01a96fb51a9cdd3a4e613147c65f6d99b9c69))
* **transfer:** implement X25519 ECDH text encryption primitives ([6ade785](https://github.com/pandaymx/lanchat/commit/6ade785b00e5545546824c8a35ce1ed353be1d6d))
* **ui-android:** render inline bitmap thumbnails and voice/fingerprint controls ([28eb240](https://github.com/pandaymx/lanchat/commit/28eb240bc427b4c756ffe781a67b9208692ce3af))
* **ui-ios:** render inline UIImage thumbnails and voice/fingerprint controls ([73262c7](https://github.com/pandaymx/lanchat/commit/73262c777be2d513f3675455efb14de2bc24fc19))
* **ui-linux:** render inline image thumbnails and voice/fingerprint actions ([c40ffb0](https://github.com/pandaymx/lanchat/commit/c40ffb0643115bf13078bba92713f41e9fd3667c))
* **ui-macos:** render inline NSImage thumbnails and voice/fingerprint controls ([d677c57](https://github.com/pandaymx/lanchat/commit/d677c570cca9f84dd8d34376818a8f01dc2ea233))
* **ui-win:** add inline image preview, bubble context menu, and voice/fingerprint tools ([d0da60b](https://github.com/pandaymx/lanchat/commit/d0da60b07e0a50665bb7ca528c874fb4219a5a6e))


### Bug Fixes

* **core:** remove unused errEmptyAddress sentinel after M14 auto mode ([0175407](https://github.com/pandaymx/lanchat/commit/0175407c7c5e69c2bf8f4ca5369a9184e40b89d7))

## [1.6.1](https://github.com/pandaymx/lanchat/compare/v1.5.0...v1.6.1) (2026-10-25)


### Features

* **ui-android:** redesign Compose interface with mobile QQ layout ([75eb39c](https://github.com/pandaymx/lanchat/commit/75eb39c11be686f4b35af7224eb5cb9302aad593))
* **ui-ios:** redesign SwiftUI interface with mobile QQ layout ([d16c88d](https://github.com/pandaymx/lanchat/commit/d16c88df69cbe81992113325bc43a1e63536e1c8))
* **ui-linux:** redesign GTK4 interface with desktop QQ 4-column layout ([333b11c](https://github.com/pandaymx/lanchat/commit/333b11c8697f3692379a1182efc540df5cab506d))
* **ui-macos:** redesign SwiftUI interface with desktop QQ 4-column layout ([94aaaa2](https://github.com/pandaymx/lanchat/commit/94aaaa2cf24b12252583d47455cc5c0c31bd94fd))
* **ui-win:** redesign WinUI 3 interface with desktop QQ 4-column layout ([247d985](https://github.com/pandaymx/lanchat/commit/247d985c3c1c3082e8a1f16727109182e2fc8f88))


### Bug Fixes

* **ui-ios:** use Section header/footer ViewBuilder overload in CreateChannelView ([598a1c8](https://github.com/pandaymx/lanchat/commit/598a1c894911419738fdccc9178456cc8279f915))

## [1.5.0](https://github.com/pandaymx/lanchat/compare/v1.4.0...v1.5.0) (2026-10-25)


### Features

* **ci:** build and publish standalone lanchat signaling server binaries ([d16e634](https://github.com/pandaymx/lanchat/commit/d16e6344db7b63e31b96d9dce5fff67bbdfe00cf))
* **core:** add end-to-end AES-256-GCM text encryption (/e2ee) ([6eed598](https://github.com/pandaymx/lanchat/commit/6eed598dbc3ded76f45f4604480d11eb94681c70))
* **core:** add static server probing, quote reply, and auto server bootstrap ([eb9c80f](https://github.com/pandaymx/lanchat/commit/eb9c80f0ada7055f533a36c90f050f187ecfa4c7))
* **server:** add /healthz, /api/status, and /status monitoring dashboard ([8a1ff88](https://github.com/pandaymx/lanchat/commit/8a1ff881647c825cde0a4cc35ca40293ff1b0a8b))
* **ui-android:** support /search, /reply quote formatting, and /e2ee status ([8795cb8](https://github.com/pandaymx/lanchat/commit/8795cb8f15f179b7b6252277e8ecda5cdf8153e3))
* **ui-ios:** support /search, /reply quote formatting, and /e2ee status ([3f3a01d](https://github.com/pandaymx/lanchat/commit/3f3a01d92d2149cfcd4a328a5bcfc4446fee30bc))
* **ui-linux:** support /search, /reply quote formatting, and /e2ee status ([cf91cf5](https://github.com/pandaymx/lanchat/commit/cf91cf541ac886469b8dd0c88ce36c9c604b44d6))
* **ui-macos:** support /search, /reply quote formatting, and /e2ee status ([cb4da11](https://github.com/pandaymx/lanchat/commit/cb4da11b0582f62126d84713448ddaab07c7190c))
* **ui-win:** support /search, /reply quote formatting, and /e2ee status ([6f61723](https://github.com/pandaymx/lanchat/commit/6f61723922d2fba115954147e11c3146c82bebe2))

## [1.4.0](https://github.com/pandaymx/lanchat/compare/v1.3.0...v1.4.0) (2026-10-25)


### Features

* **ci:** build and publish Ubuntu deb and Red Hat rpm packages ([dbf8a1d](https://github.com/pandaymx/lanchat/commit/dbf8a1d20a04d750479ee531fd3c9feb5971ca0c))
* **ui-linux:** add desktop entry, icon, and deb/rpm packaging script ([9dcf57b](https://github.com/pandaymx/lanchat/commit/9dcf57bc9f68deff9a205680eda8c8272722ddc3))

## [1.3.0](https://github.com/pandaymx/lanchat/compare/v1.2.0...v1.3.0) (2026-10-25)


### Features

* **core:** support directory auto-zip and clipboard image URI transfers ([158da8f](https://github.com/pandaymx/lanchat/commit/158da8fe220c0eab59e2406285e99239cce494d8))
* **ui-android:** render message revoke and read receipt states ([ac54512](https://github.com/pandaymx/lanchat/commit/ac54512d423bead5b4fecd839ffecd612a622993))
* **ui-ios:** render message revoke and read receipt states ([181427f](https://github.com/pandaymx/lanchat/commit/181427f60921c78881fb9989df2f0dd6c9aca572))
* **ui-linux:** render message revoke and read receipt states ([4d9ffb8](https://github.com/pandaymx/lanchat/commit/4d9ffb8e63b4cb8c0083b3756884a2a8b073494a))
* **ui-macos:** support folder transfer and render revoke/read states ([a42b874](https://github.com/pandaymx/lanchat/commit/a42b8742fa138f86d9291dcc4c9cc8555978e5ed))
* **ui-win:** render message revoke and read receipt states ([a94b935](https://github.com/pandaymx/lanchat/commit/a94b9350f4faf7d7b757ad12166522f58683de87))


### Bug Fixes

* **core:** ensure group fileKey arrives before inbound block handshake ([cefe1db](https://github.com/pandaymx/lanchat/commit/cefe1db576edbf0c59392cdabe76583b2a7bd8e2))

## [1.2.0](https://github.com/pandaymx/lanchat/compare/v1.1.0...v1.2.0) (2026-10-23)


### Features

* **appapi:** add optional revoked and readBy fields to ChatMessage ([d31f711](https://github.com/pandaymx/lanchat/commit/d31f7116ca30898109971d911ff3aa1b96e7024b))
* **core:** implement message revoke, read receipts, and history search ([acdd629](https://github.com/pandaymx/lanchat/commit/acdd62905baa3219bc42641e092a8677b06ab5a5))
* **protocol:** add MSG_READ and MSG_REVOKE control message types and payloads ([b190a81](https://github.com/pandaymx/lanchat/commit/b190a817162e8cb876e0e52f4341fc7c4f73424c))
* **server:** route MSG_READ and MSG_REVOKE across unicast and group channels ([7efd046](https://github.com/pandaymx/lanchat/commit/7efd046d123816e44960a3ee35888e45a11e843d))


### Bug Fixes

* **ci:** use RELEASE_PLEASE_TOKEN in release-please workflow ([2fd3264](https://github.com/pandaymx/lanchat/commit/2fd32643aabf811908bb8091150ee391cce63b23))

## [1.1.0](https://github.com/pandaymx/lanchat/compare/v1.0.0...v1.1.0) (2026-10-23)


### Features

* **appapi:** expose persisted ChatMessage history in State snapshot ([3b493c8](https://github.com/pandaymx/lanchat/commit/3b493c8eeea449425df69f14e19eabfbefd37312))
* **ci:** 三端原生构建+单测门禁（Win/macOS/Android） ([#115](https://github.com/pandaymx/lanchat/issues/115)) ([ff44e86](https://github.com/pandaymx/lanchat/commit/ff44e86cc141358b9898668087aa7552d42c1c44))
* **ci:** 性能基线复测门禁 + 每日定时 ([#116](https://github.com/pandaymx/lanchat/issues/116)) ([365d3bf](https://github.com/pandaymx/lanchat/commit/365d3bfa425024bb4b31c559dad55e0207d11dd2))
* **ci:** 自定义 release notes + 未签名安全提示 ([#117](https://github.com/pandaymx/lanchat/issues/117)) ([e9faac1](https://github.com/pandaymx/lanchat/commit/e9faac14321bc1cffde282badf642e98faf5c866))
* **core:** implement pause/resume, group cancel, and JSONL chat history ([844dfe7](https://github.com/pandaymx/lanchat/commit/844dfe7af0deee9e27bf0cf43a4e0ec1884761a5))
* **protocol:** add optional replyTo and mentions to TextPayload ([221fe32](https://github.com/pandaymx/lanchat/commit/221fe3203fa37ca88c530677fb76fa3c0dc49f7c))
* **server:** support group channel routing for sticker messages ([f2a8a5a](https://github.com/pandaymx/lanchat/commit/f2a8a5a7294f12cf047ce5078e801817af95af0c))
* **ui-android:** reach 18/18 contract parity and render group.matrix ([389ea3d](https://github.com/pandaymx/lanchat/commit/389ea3d5966d6e85f96abfd8fae7b36cfcc4c602))
* **ui-ios:** reach 18/18 contract parity and render group.matrix ([ff6b152](https://github.com/pandaymx/lanchat/commit/ff6b15234b0920a2e6898c38ea6a160bc2304b29))
* **ui-linux:** reach 18/18 contract parity and render group.matrix ([6215d03](https://github.com/pandaymx/lanchat/commit/6215d0323fb684f4c9de2bd9d2d0f900fb5b4651))
* **ui-macos:** reach 18/18 contract parity and render group.matrix ([cf4a718](https://github.com/pandaymx/lanchat/commit/cf4a7185ff29584c4654390458734b63cb705965))
* **ui-win:** reach 18/18 contract parity and render group.matrix ([70bb23b](https://github.com/pandaymx/lanchat/commit/70bb23be7d2e3ba28d192f8810e675997a7a7893))


### Bug Fixes

* **ci:** checkout repo in release publish job and pin gomobile version ([5c31c16](https://github.com/pandaymx/lanchat/commit/5c31c1647384cafaa80af9ad44b2d4e7c0151389))
* **core:** remove unused errPauseUnsupported sentinel error ([cf4605b](https://github.com/pandaymx/lanchat/commit/cf4605bcd359ba761f56e994e7ff81955f858e90))
* **ui-linux:** fix base64 temporary borrow and clippy lints in models ([03a88c3](https://github.com/pandaymx/lanchat/commit/03a88c3927dea0af78b781741db4f9e2dbbb29bc))

## [0.24.0](https://github.com/pandaymx/lanchat/compare/v0.23.4...v0.24.0) (2026-10-03)


### Features

* **ci:** 契约测试 — schema × 五端方法清单自动校验 ([#114](https://github.com/pandaymx/lanchat/issues/114)) ([394254e](https://github.com/pandaymx/lanchat/commit/394254e30cb08bf59348fd6a1e1649e75fe28fba))

## [0.23.4](https://github.com/pandaymx/lanchat/compare/v0.23.3...v0.23.4) (2026-10-02)


### Performance Improvements

* **ci:** Release 启用 Gradle 缓存并固定 gomobile 版本 ([#110](https://github.com/pandaymx/lanchat/issues/110)) ([81df3c8](https://github.com/pandaymx/lanchat/commit/81df3c8d78494a657845153d83084a557ca3ab88))
* **ci:** Release 的 Linux 作业改用系统 GTK4 ([#112](https://github.com/pandaymx/lanchat/issues/112)) ([a9a2751](https://github.com/pandaymx/lanchat/commit/a9a27515a3b3481434ebeafe69d2c52cbf5726eb))
* **ci:** Windows 作业复用 daemon 制品，全作业补 timeout-minutes ([#113](https://github.com/pandaymx/lanchat/issues/113)) ([c18ce4e](https://github.com/pandaymx/lanchat/commit/c18ce4ea762f853e3cac92f9e4f79ecaa110a766))

## [0.23.3](https://github.com/pandaymx/lanchat/compare/v0.23.2...v0.23.3) (2026-10-02)


### Bug Fixes

* **ui-ios:** 修大文件弹窗捕获错误 + 补 PR 级 Swift 编译门禁 ([#108](https://github.com/pandaymx/lanchat/issues/108)) ([c5807b4](https://github.com/pandaymx/lanchat/commit/c5807b4908120bb5efb7e2b3a8e9bb85aa74848a))

## [0.23.2](https://github.com/pandaymx/lanchat/compare/v0.23.1...v0.23.2) (2026-10-02)


### Bug Fixes

* **ui-android:** 修编译错误并在 PR 阶段加 Android 编译门禁 ([#104](https://github.com/pandaymx/lanchat/issues/104)) ([b4db13b](https://github.com/pandaymx/lanchat/commit/b4db13b351d0971ad788d2133aede68b202464ae))

## [0.23.1](https://github.com/pandaymx/lanchat/compare/v0.23.0...v0.23.1) (2026-10-02)


### Bug Fixes

* **ui-ios:** 频道方法补 NSErrorPointer，onChange 回退 iOS 16 API ([#102](https://github.com/pandaymx/lanchat/issues/102)) ([d5830b0](https://github.com/pandaymx/lanchat/commit/d5830b01563759295f946b32e2eb31daeb70834d))

## [0.23.0](https://github.com/pandaymx/lanchat/compare/v0.22.0...v0.23.0) (2026-10-02)


### Features

* **ui-win:** G2 channels with group chat [M9] ([#88](https://github.com/pandaymx/lanchat/issues/88)) ([563597d](https://github.com/pandaymx/lanchat/commit/563597d1d80ca91d07acc0d88002ee4c03f689e7))

## [0.22.0](https://github.com/pandaymx/lanchat/compare/v0.21.1...v0.22.0) (2026-10-02)


### Features

* **discover:** serve 注册 mDNS 广告，browse 秒级可见 [M2] ([#98](https://github.com/pandaymx/lanchat/issues/98)) ([f471f4f](https://github.com/pandaymx/lanchat/commit/f471f4f312acea2b630d48aa492bd9a7577d934f))

## [0.21.1](https://github.com/pandaymx/lanchat/compare/v0.21.0...v0.21.1) (2026-10-02)


### Bug Fixes

* **ci:** 预检 Android 签名凭据并在失效时降级 debug 包 ([#96](https://github.com/pandaymx/lanchat/issues/96)) ([8edde63](https://github.com/pandaymx/lanchat/commit/8edde634f1b2d182d8f4821862d5d506a9428ef8))

## [0.21.0](https://github.com/pandaymx/lanchat/compare/v0.20.0...v0.21.0) (2026-10-02)


### Features

* **ui-ios:** add G2 channels with group chat and large-file boundary [M9] ([#90](https://github.com/pandaymx/lanchat/issues/90)) ([3552660](https://github.com/pandaymx/lanchat/commit/3552660053315ed7e992ce71e3885568bd6aa7e8))

## [0.20.0](https://github.com/pandaymx/lanchat/compare/v0.19.0...v0.20.0) (2026-10-02)


### Features

* **ui-macos:** add G2 channel list, create/join/invite/leave and group chat [M9] ([#89](https://github.com/pandaymx/lanchat/issues/89)) ([719d74b](https://github.com/pandaymx/lanchat/commit/719d74b23635a63dcd8a0fd22fc3f21dd8757133))

## [0.19.0](https://github.com/pandaymx/lanchat/compare/v0.18.0...v0.19.0) (2026-10-02)


### Features

* **ui-android:** add G2 channels with group chat and foreground transfer [M9] ([#91](https://github.com/pandaymx/lanchat/issues/91)) ([687241b](https://github.com/pandaymx/lanchat/commit/687241b5293280b3ffd491a338d9b8c2cea9fada))

## [0.18.0](https://github.com/pandaymx/lanchat/compare/v0.17.15...v0.18.0) (2026-10-02)


### Features

* **ui-linux:** add G2 channel list, create/join/invite/leave and group chat [M9] ([#92](https://github.com/pandaymx/lanchat/issues/92)) ([a2af8e7](https://github.com/pandaymx/lanchat/commit/a2af8e7528840ca9efb6e19d589249aa2409d46e))

## [0.17.15](https://github.com/pandaymx/lanchat/compare/v0.17.14...v0.17.15) (2026-10-02)


### Bug Fixes

* **ui-android:** gomobile 字段改显式 getter 并解码 browseServersJSON ([#85](https://github.com/pandaymx/lanchat/issues/85)) ([364f8b2](https://github.com/pandaymx/lanchat/commit/364f8b2f7e54d7604761015ac503ee854e9f0c04))

## [0.17.14](https://github.com/pandaymx/lanchat/compare/v0.17.13...v0.17.14) (2026-10-02)


### Bug Fixes

* **ui-ios:** 线程包装闭包参数补 [@escaping](https://github.com/escaping) ([#84](https://github.com/pandaymx/lanchat/issues/84)) ([285ff7a](https://github.com/pandaymx/lanchat/commit/285ff7a2d55c4e0e24ad6971ba6fe532c7646095))

## [0.17.13](https://github.com/pandaymx/lanchat/compare/v0.17.12...v0.17.13) (2026-10-02)


### Bug Fixes

* **ui-win:** MSI unpackaged 直接拉起 daemon，避开包标识 API ([#82](https://github.com/pandaymx/lanchat/issues/82)) ([1151680](https://github.com/pandaymx/lanchat/commit/1151680702e846a2f820cad91a95b2d988b5278b))

## [0.17.12](https://github.com/pandaymx/lanchat/compare/v0.17.11...v0.17.12) (2026-10-02)


### Bug Fixes

* **ui-ios:** 修正 init 首参数标签与 getState 闭包参数 ([#80](https://github.com/pandaymx/lanchat/issues/80)) ([db3b75f](https://github.com/pandaymx/lanchat/commit/db3b75f2f2d34d0e7580a2d472a2662f107f6776))

## [0.17.11](https://github.com/pandaymx/lanchat/compare/v0.17.10...v0.17.11) (2026-10-02)


### Bug Fixes

* **ui-android:** gomobile Peer/Transfer 字段访问名改为 ID/OS ([#76](https://github.com/pandaymx/lanchat/issues/76)) ([d5e22c1](https://github.com/pandaymx/lanchat/commit/d5e22c18da96832ef2a913af5e619021e460c541))

## [0.17.10](https://github.com/pandaymx/lanchat/compare/v0.17.9...v0.17.10) (2026-10-02)


### Bug Fixes

* **ui-android:** browseServersJSON 需作为方法调用 ([#75](https://github.com/pandaymx/lanchat/issues/75)) ([4663617](https://github.com/pandaymx/lanchat/commit/4663617cf2f0c74668a3f52a888032de3bbe31d4))

## [0.17.9](https://github.com/pandaymx/lanchat/compare/v0.17.8...v0.17.9) (2026-10-02)


### Bug Fixes

* **ci:** MSIX 构建注释移出 PowerShell 续行块 ([#73](https://github.com/pandaymx/lanchat/issues/73)) ([cbe9838](https://github.com/pandaymx/lanchat/commit/cbe9838699bfb88839989e62c147850d0558a816))

## [0.17.8](https://github.com/pandaymx/lanchat/compare/v0.17.7...v0.17.8) (2026-10-02)


### Bug Fixes

* **ci:** 安装 platforms;android-37.0 而非 android-37 ([#71](https://github.com/pandaymx/lanchat/issues/71)) ([898dbc7](https://github.com/pandaymx/lanchat/commit/898dbc788fdb3607741869222c4291f5632b0e97))

## [0.17.7](https://github.com/pandaymx/lanchat/compare/v0.17.6...v0.17.7) (2026-10-02)


### Bug Fixes

* **ui-android:** bump compileSdk to 37 for androidx 2.10 requirements ([#69](https://github.com/pandaymx/lanchat/issues/69)) ([d23af6b](https://github.com/pandaymx/lanchat/commit/d23af6be2c2b7865becef538a5a6fd9b6d718d79))

## [0.17.6](https://github.com/pandaymx/lanchat/compare/v0.17.5...v0.17.6) (2026-10-02)


### Bug Fixes

* **ci:** disable appinstaller generation for sideload MSIX ([#65](https://github.com/pandaymx/lanchat/issues/65)) ([e5b3dd1](https://github.com/pandaymx/lanchat/commit/e5b3dd101ab6260bfe5e9f9e86f98dba5d34ea4b))

## [0.17.5](https://github.com/pandaymx/lanchat/compare/v0.17.4...v0.17.5) (2026-10-02)


### Bug Fixes

* **ui-android:** drop kotlin.android plugin for AGP 9 built-in Kotlin ([#64](https://github.com/pandaymx/lanchat/issues/64)) ([d879cc5](https://github.com/pandaymx/lanchat/commit/d879cc52ccb012afd88c65c5fddbaee20b71dd22))

## [0.17.4](https://github.com/pandaymx/lanchat/compare/v0.17.3...v0.17.4) (2026-10-02)


### Bug Fixes

* **ci:** remove -arch flag from iOS simulator xcodebuild ([#63](https://github.com/pandaymx/lanchat/issues/63)) ([eeef609](https://github.com/pandaymx/lanchat/commit/eeef60912c64e21b4d49aac0bdec804cf15c9f57))

## [0.17.3](https://github.com/pandaymx/lanchat/compare/v0.17.2...v0.17.3) (2026-10-02)


### Bug Fixes

* **ci:** complete linux gtk deps and correct msix cert properties ([#58](https://github.com/pandaymx/lanchat/issues/58)) ([53001f7](https://github.com/pandaymx/lanchat/commit/53001f7c41d37a774a55df02d0273e8cd26e3c14))

## [0.17.2](https://github.com/pandaymx/lanchat/compare/v0.17.1...v0.17.2) (2026-10-02)


### Bug Fixes

* **ui-ios:** decode JSON bindings payloads with JSONDecoder ([#57](https://github.com/pandaymx/lanchat/issues/57)) ([8484591](https://github.com/pandaymx/lanchat/commit/84845914419d87da1ede0213afb02127cecfd0b7))

## [0.17.1](https://github.com/pandaymx/lanchat/compare/v0.17.0...v0.17.1) (2026-10-02)


### Bug Fixes

* **bindings:** use JSON bytes for slice types to unblock gomobile ([#56](https://github.com/pandaymx/lanchat/issues/56)) ([81a5e51](https://github.com/pandaymx/lanchat/commit/81a5e51f56ea9251de2b53e2aabfd6583bcaca0a))

## [0.17.0](https://github.com/pandaymx/lanchat/compare/v0.16.3...v0.17.0) (2026-10-02)


### Features

* **group:** encrypt swarm data plane with AES-GCM and relay fallback [M9] ([#54](https://github.com/pandaymx/lanchat/issues/54)) ([36b5d30](https://github.com/pandaymx/lanchat/commit/36b5d303408f6efbafce78ec8fe82f8d9d285ca6))

## [0.16.3](https://github.com/pandaymx/lanchat/compare/v0.16.2...v0.16.3) (2026-10-02)


### Bug Fixes

* **ci:** repair release build failures for windows, android, linux, ios ([#49](https://github.com/pandaymx/lanchat/issues/49)) ([20aebbe](https://github.com/pandaymx/lanchat/commit/20aebbe379ccfa25d2c994680b88c816d9cd69a1))

## [0.16.2](https://github.com/pandaymx/lanchat/compare/v0.16.1...v0.16.2) (2026-10-02)


### Bug Fixes

* **ci:** repair android sdk, macos runner and gomobile dependency ([#47](https://github.com/pandaymx/lanchat/issues/47)) ([84628e9](https://github.com/pandaymx/lanchat/commit/84628e988769466a9bca09f28f1cb3ce482718cc))

## [0.16.1](https://github.com/pandaymx/lanchat/compare/v0.16.0...v0.16.1) (2026-10-01)


### Bug Fixes

* **ci:** checkout repository in resolve job ([#45](https://github.com/pandaymx/lanchat/issues/45)) ([a5b31df](https://github.com/pandaymx/lanchat/commit/a5b31dfb2c03bc09b114a2409538337bfb17419a))

## [0.16.0](https://github.com/pandaymx/lanchat/compare/v0.15.0...v0.16.0) (2026-10-01)


### Features

* **group:** complete G2 private channels with invite and leave [M9] ([#41](https://github.com/pandaymx/lanchat/issues/41)) ([4554835](https://github.com/pandaymx/lanchat/commit/45548359fe58655aada8f2eba5eaf28fa386333d))

## [0.15.0](https://github.com/pandaymx/lanchat/compare/v0.14.0...v0.15.0) (2026-10-01)


### Features

* **ui-win:** package app as MSIX for Microsoft Store ([#38](https://github.com/pandaymx/lanchat/issues/38)) ([6e1e57f](https://github.com/pandaymx/lanchat/commit/6e1e57f76f394b905fb674fc070938ef276f2578))

## [0.14.0](https://github.com/pandaymx/lanchat/compare/v0.13.0...v0.14.0) (2026-10-01)


### Features

* **core:** wire group file swarm data plane [M9] ([#36](https://github.com/pandaymx/lanchat/issues/36)) ([5cfb99c](https://github.com/pandaymx/lanchat/commit/5cfb99c9149b008467cab729c85f026adbe7b8c3))

## [0.13.0](https://github.com/pandaymx/lanchat/compare/v0.12.0...v0.13.0) (2026-09-30)


### Features

* **protocol:** add candidates and token to group signaling [M9] ([#34](https://github.com/pandaymx/lanchat/issues/34)) ([9b930e8](https://github.com/pandaymx/lanchat/commit/9b930e8f497594d0daa32e3d1a836fa8d95e979f))

## [0.12.0](https://github.com/pandaymx/lanchat/compare/v0.11.0...v0.12.0) (2026-09-30)


### Features

* **core:** wire group text fanout and G2 channels [M9] ([#32](https://github.com/pandaymx/lanchat/issues/32)) ([93153cd](https://github.com/pandaymx/lanchat/commit/93153cd03dfdf7ad7b254099e8be30f0fed1dcb5))

## [0.11.0](https://github.com/pandaymx/lanchat/compare/v0.10.0...v0.11.0) (2026-09-30)


### Features

* M6 Android 原生端 ([#30](https://github.com/pandaymx/lanchat/issues/30)) ([3dbb7df](https://github.com/pandaymx/lanchat/commit/3dbb7dfc1ed3dff84ac67608825ca9df06b5b796))

## [0.10.0](https://github.com/pandaymx/lanchat/compare/v0.9.1...v0.10.0) (2026-09-30)


### Features

* **group:** M9 群组广播 G1 swarm + G2 频道 ([#28](https://github.com/pandaymx/lanchat/issues/28)) ([2bb46c5](https://github.com/pandaymx/lanchat/commit/2bb46c5e823457bb43e62397aa1fb5592762332f))

## [0.9.1](https://github.com/pandaymx/lanchat/compare/v0.9.0...v0.9.1) (2026-09-29)


### Bug Fixes

* **core:** 清理 server/core 存量 lint 告警并补 core 责任田 ([#25](https://github.com/pandaymx/lanchat/issues/25)) ([592de35](https://github.com/pandaymx/lanchat/commit/592de35364d8cef507c6ab05099814778edb45ca))

## [0.9.0](https://github.com/pandaymx/lanchat/compare/v0.8.0...v0.9.0) (2026-09-29)


### Features

* M8 iOS 原生端 ([#23](https://github.com/pandaymx/lanchat/issues/23)) ([335dacb](https://github.com/pandaymx/lanchat/commit/335dacbc4fc319fedde5d8ed11627f8a82517367))

## [0.8.0](https://github.com/pandaymx/lanchat/compare/v0.7.1...v0.8.0) (2026-09-29)


### Features

* M7 macOS 原生端 ([#20](https://github.com/pandaymx/lanchat/issues/20)) ([640c57d](https://github.com/pandaymx/lanchat/commit/640c57d5469d76e53aa666f52d87cabb090463d0))

## [0.7.1](https://github.com/pandaymx/lanchat/compare/v0.7.0...v0.7.1) (2026-09-29)


### Bug Fixes

* **core:** emit connected event on initial connect ([#15](https://github.com/pandaymx/lanchat/issues/15)) ([ef6ce07](https://github.com/pandaymx/lanchat/commit/ef6ce0708172052c23d16081077c268fc926292e))

## [0.7.0](https://github.com/pandaymx/lanchat/compare/v0.6.0...v0.7.0) (2026-09-29)


### Features

* **ui-linux:** M5 Linux 原生端（GTK4 + libadwaita） ([#13](https://github.com/pandaymx/lanchat/issues/13)) ([7131530](https://github.com/pandaymx/lanchat/commit/713153038a9d1c06887629bb98dc25f45a1e315f))

## [0.6.0](https://github.com/pandaymx/lanchat/compare/v0.5.0...v0.6.0) (2026-09-29)


### Features

* **ui-win:** M4 Windows native client ([#11](https://github.com/pandaymx/lanchat/issues/11)) ([f381338](https://github.com/pandaymx/lanchat/commit/f3813386f80e67f7881d9335be0cf3d9879cba07))

## [0.5.0](https://github.com/pandaymx/lanchat/compare/v0.4.0...v0.5.0) (2026-09-29)


### Features

* **core:** M4 客户端核心、daemon 进程与 JSON-RPC IPC ([#8](https://github.com/pandaymx/lanchat/issues/8)) ([afc5728](https://github.com/pandaymx/lanchat/commit/afc5728ad4773eda266c4a9fd7357713b02b737a))


### Bug Fixes

* **core:** 修正 TestPeerRoster 等待错误对象导致的偶发失败 ([#10](https://github.com/pandaymx/lanchat/issues/10)) ([d58cd2d](https://github.com/pandaymx/lanchat/commit/d58cd2df451da98c3f3c1258ca572fae06f56b95))

## [0.4.0](https://github.com/pandaymx/lanchat/compare/v0.3.0...v0.4.0) (2026-09-28)


### Features

* **core:** M3 可靠性增强与 AES-GCM 中继 ([#6](https://github.com/pandaymx/lanchat/issues/6)) ([2276683](https://github.com/pandaymx/lanchat/commit/2276683dcd26a4a6de486559356efad45bdc6606))

## [0.3.0](https://github.com/pandaymx/lanchat/compare/v0.2.0...v0.3.0) (2026-09-27)


### Features

* M2 mDNS 零配置发现 + P2P 主干传输 ([#3](https://github.com/pandaymx/lanchat/issues/3)) ([23a3628](https://github.com/pandaymx/lanchat/commit/23a3628518ab276ec5c87237ace3500bba01e2ac))

## [0.2.0](https://github.com/pandaymx/lanchat/compare/lanchat-v0.1.1...lanchat-v0.2.0) (2026-09-27)


### Features

* **config:** add server config loader with flag env yaml precedence ([1f9e51b](https://github.com/pandaymx/lanchat/commit/1f9e51b10d5cba9ab84dec124086a0ac15eb48b5))
* **core:** wire serve genpsk and version subcommands to server ([2707559](https://github.com/pandaymx/lanchat/commit/27075594878cc1fe046e4be143c45331a6a71e91))
* **server:** add signaling hub registry router with psk heartbeat and text ([1be9137](https://github.com/pandaymx/lanchat/commit/1be9137b9c973dec05919f6ba8474778d8826e1b))

## [0.1.1](https://github.com/pandaymx/lanchat/compare/lanchat-v0.1.0...lanchat-v0.1.1) (2026-09-27)


### Features

* **appapi:** add appapi stubs, ipc schema v1 and go ci ([5cc79e0](https://github.com/pandaymx/lanchat/commit/5cc79e091f30f560d07591f51a19566991151a88))
* **protocol:** add envelope and control message types ([ec892a6](https://github.com/pandaymx/lanchat/commit/ec892a63a921fd323943d95b3fbdb057ba48d564))
* **protocol:** add lctp frame, framer and fuzz tests ([169fd17](https://github.com/pandaymx/lanchat/commit/169fd170f4d76986ac6eb5c4dc96c98cb4e5fdcf))
* **protocol:** add protocol version constants ([aa5bad0](https://github.com/pandaymx/lanchat/commit/aa5bad0049d5b759d6b8cf7ffa337de6a62344a7))
