-- 允许连接的最低客户端版本号，编码为 major*1000000 + minor*1000 + patch。
-- 0 表示不启用版本门禁。放在数据库而不是环境变量：只更新客户端时也能
-- 在管理界面里调整，不必登服务器改 env 并重建容器。
ALTER TABLE server_settings
    ADD COLUMN minimum_client_version integer NOT NULL DEFAULT 0
        CHECK (minimum_client_version >= 0);
