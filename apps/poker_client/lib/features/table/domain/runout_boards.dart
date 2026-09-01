/// 发两次时，两块牌面共享全下前已发出的公共牌前缀；只有前缀之后的牌
/// 属于各自的发牌结果，需要在展示切换时渐隐/淡入。
int sharedRunoutPrefixLength(List<List<String>> boards) {
  if (boards.length < 2) return 0;
  final first = boards[0];
  final second = boards[1];
  var length = 0;
  while (length < first.length &&
      length < second.length &&
      first[length] == second[length]) {
    length++;
  }
  return length;
}
