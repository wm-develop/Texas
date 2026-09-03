import { media } from '@kit.MediaKit';
import { audio } from '@kit.AudioKit';
import fs from '@ohos.file.fs';

/// 牌局提示音的低时延播放通道。
///
/// 起因：audioplayers 在鸿蒙上播放前会调用
/// setAudioSessionScene(AUDIO_SESSION_SCENE_MEDIA) 与 activateAudioSession，
/// 这是应用级的音频会话切换，会压制同一应用里 TRTC 的通话流——提示音一响
/// 就听不到其他玩家说话。把并发模式改成混音也无济于事，因为致命的是会话
/// 切换本身。
///
/// SoundPool 是系统为「急促简短的音效」提供的低时延通道，完全不经过
/// AudioSessionManager；并且按官方说明，AudioRendererInfo 的 usage 取
/// STREAM_USAGE_MUSIC 时为混音模式，不会打断其他音频播放。
///
/// 单个音效需小于 1MB，牌局提示音都在零点几秒、几十 KB 量级。
export class TableSoundPool {
  private static readonly MAX_STREAMS: number = 4;

  private pool: media.SoundPool | null = null;
  private creating: Promise<media.SoundPool> | null = null;
  private soundIds: Map<string, number> = new Map();
  private loaded: Set<number> = new Set();
  private pendingLoads: Map<number, () => void> = new Map();

  async play(path: string, volume: number): Promise<void> {
    const pool = await this.ensurePool();
    const soundId = await this.soundIdFor(pool, path);
    const level = Math.max(0, Math.min(1, volume));
    await pool.play(soundId, {
      loop: 0,
      rate: audio.AudioRendererRate.RENDER_RATE_NORMAL,
      leftVolume: level,
      rightVolume: level,
      priority: 0
    });
  }

  async release(): Promise<void> {
    const pool = this.pool;
    this.pool = null;
    this.creating = null;
    this.soundIds.clear();
    this.loaded.clear();
    this.pendingLoads.clear();
    if (pool !== null) {
      await pool.release();
    }
  }

  private ensurePool(): Promise<media.SoundPool> {
    const ready = this.pool;
    if (ready !== null) {
      return Promise.resolve(ready);
    }
    let creating = this.creating;
    if (creating === null) {
      creating = this.createPool();
      this.creating = creating;
    }
    return creating;
  }

  private async createPool(): Promise<media.SoundPool> {
    // usage 取 MUSIC：官方说明此时为混音模式，不会打断其他音频播放。
    const rendererInfo: audio.AudioRendererInfo = {
      usage: audio.StreamUsage.STREAM_USAGE_MUSIC,
      rendererFlags: 0
    };
    const pool = await media.createSoundPool(
      TableSoundPool.MAX_STREAMS,
      rendererInfo
    );
    pool.on('loadComplete', (soundId: number): void => {
      this.loaded.add(soundId);
      const waiting = this.pendingLoads.get(soundId);
      if (waiting !== undefined) {
        this.pendingLoads.delete(soundId);
        waiting();
      }
    });
    this.pool = pool;
    return pool;
  }

  private async soundIdFor(
    pool: media.SoundPool,
    path: string
  ): Promise<number> {
    const cached = this.soundIds.get(path);
    if (cached !== undefined) {
      return cached;
    }
    const file = await fs.open(path, fs.OpenMode.READ_ONLY);
    try {
      const soundId = await pool.load('fd://' + file.fd);
      await this.awaitLoaded(soundId);
      this.soundIds.set(path, soundId);
      return soundId;
    } finally {
      await fs.close(file);
    }
  }

  /// 必须等 loadComplete 才能播放。load 的 Promise 有可能晚于回调完成，
  /// 因此先查已完成集合再挂等待；再加一道超时，避免回调不来时永远卡住。
  private awaitLoaded(soundId: number): Promise<void> {
    if (this.loaded.has(soundId)) {
      return Promise.resolve();
    }
    return new Promise<void>((resolve: () => void): void => {
      let settled = false;
      const finish = (): void => {
        if (settled) {
          return;
        }
        settled = true;
        this.pendingLoads.delete(soundId);
        resolve();
      };
      this.pendingLoads.set(soundId, finish);
      setTimeout(finish, 2000);
    });
  }
}
