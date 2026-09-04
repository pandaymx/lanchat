export default {
  extends: ['@commitlint/config-conventional'],
  rules: {
    // scope 白名单：逼着每次提交想清楚改的是哪一层
    // 与 AGENTS.md §5.1 保持一致，改这里要同步改文档
    'scope-enum': [
      2,
      'always',
      [
        'core', // pkg/core 跨端内核
        'proto', // 协议
        'hub', // 服务端
        'tui', // 终端端
        'web', // Web 端
        'desktop', // 桌面端
        'mobile', // 移动端
        'store', // 持久化
        'deps', // 依赖变更
        'ci', // CI / 构建流程
        'docs', // 文档
        'repo', // 仓库配置（钩子/lint/编辑器）
        'release', // 发布相关
      ],
    ],
    'subject-max-length': [2, 'always', 72],
    'subject-case': [0],
    'body-max-line-length': [0],
    'footer-max-line-length': [0],
  },
};
