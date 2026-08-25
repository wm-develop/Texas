import 'dart:convert';
import 'dart:io';

import 'package:web_socket_channel/web_socket_channel.dart';

Future<void> main(List<String> arguments) async {
  final serverUrl = arguments.isEmpty
      ? 'ws://127.0.0.1:8080/ws'
      : arguments.first;
  final channel = WebSocketChannel.connect(Uri.parse(serverUrl));
  await channel.ready;

  channel.sink.add(
    jsonEncode(<String, Object>{
      'version': 1,
      'type': 'system.ping',
      'requestId': 'dart-smoke-test',
    }),
  );

  final rawResponse = await channel.stream.first;
  final response = jsonDecode(rawResponse as String) as Map<String, dynamic>;
  await channel.sink.close();

  if (response['type'] != 'system.pong' ||
      response['requestId'] != 'dart-smoke-test') {
    throw StateError('Unexpected WebSocket response: $response');
  }

  stdout.writeln('WebSocket smoke test passed: ${response['type']}');
}
