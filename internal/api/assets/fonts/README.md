# 内置字体

`NotoSansSC-Black-gb.ttf`：Noto Sans SC Black (wght=900) 的子集。

- 来源：Windows 自带 `NotoSansSC-VF.ttf`，用 fontTools 实例化到 wght=900 后子集化
- 子集范围：ASCII、Latin-1、常用标点、GB2312 全部 6763 汉字及全角符号（共 7587 字符）
- 许可：SIL Open Font License 1.1，见 `OFL.txt`
- 用途：库封面「简约」样式的标题与副标题渲染

重新生成：

```bash
python -m fontTools.varLib.instancer NotoSansSC-VF.ttf wght=900 --output=full.ttf
python -m fontTools.subset full.ttf --text-file=chars.txt --layout-features='*' \
  --no-hinting --output-file=NotoSansSC-Black-gb.ttf
```
