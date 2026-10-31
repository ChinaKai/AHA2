# 3D 免费模型接入尝试记录

- 已下载免费模型：
  - `tmp/gugu3d/penguin.glb`（GitHub TTZW1001/GuguGaga，CC-BY，rigged，2.3MB，0 clip）
  - `tmp/gugu3d/whale-poly.glb`（Poly Pizza Whale，CC-BY，177KB，静态）
- 接入 Three.js 后实测：
  - penguin.glb 的 SkinnedMesh 包围盒/初始姿态异常：在宠物 64px 尺寸下只显示肚子和脚，头部在相机外；换 yaw 仍不对。
  - whale-poly.glb 是低模鲸鱼，不是鲸鱼娘；整体可显示但没有骨骼。
- 结论：这两个“免费直下”模型不适合作为最终形象；已从构建中回退，未部署。
- 线上仍是 0.5.158（提示气泡关闭、0.5.157 对话框修复）。
- 需要：Sketchfab 原版 GLB/VRM（gugugaga penguin / DeepSeek Big Fat Whale Yukkuri）或其它带正确骨骼/包围盒的模型。
