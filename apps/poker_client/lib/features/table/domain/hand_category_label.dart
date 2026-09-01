String handCategoryLabel(String category) {
  return category
      .split(RegExp(r'\s*/\s*'))
      .map(_singleHandCategoryLabel)
      .join(' / ');
}

String _singleHandCategoryLabel(String category) =>
    switch (category.trim().toLowerCase()) {
      'voluntary' => '主动亮牌',
      'private_view' => '私下查看',
      'high_card' => '高牌',
      'one_pair' => '一对',
      'two_pair' => '两对',
      'three_of_a_kind' => '三条',
      'straight' => '顺子',
      'flush' => '同花',
      'full_house' => '葫芦',
      'four_of_a_kind' => '四条',
      'straight_flush' => '同花顺',
      final value => value,
    };
