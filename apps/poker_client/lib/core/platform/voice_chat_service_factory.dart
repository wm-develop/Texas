import 'voice_chat_service.dart';
import 'voice_chat_service_web_factory.dart'
    if (dart.library.io) 'voice_chat_service_native_factory.dart'
    as implementation;

VoiceChatService createVoiceChatService() {
  return implementation.createVoiceChatService();
}
