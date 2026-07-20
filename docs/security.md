# Модель безопасности

`olcrtc-panel` работает от root, потому что управляет systemd, конфигурациями и обновлениями. Процессы olcRTC работают от `olcrtc`, Chromium - от `olcrtc-wb`.

Основные меры:

- systemctl и journalctl вызываются без shell interpolation, только для фиксированных unit и числового instance ID;
- пользовательские пути не принимаются API;
- config/key записываются во временный файл, синхронизируются и заменяются atomic rename;
- password хранится как Argon2id PHC string;
- WB token, Yandex OAuth token и proxy passwords шифруются AES-256-GCM с отдельным nonce;
- machine master key хранится вне SQLite в `/etc/olcrtc-panel/master.key` с mode `0600`;
- session и CSRF token сохраняются только в SHA-256 representation;
- cookies имеют `Secure`, `HttpOnly` для session и `SameSite=Strict`;
- mutating API требует `X-CSRF-Token`;
- login ограничен пятью неуспешными попытками за десять минут и exponential delay;
- request body ограничен 1 MiB, log query - 2000 строками;
- CSP, `nosniff`, `no-referrer`, frame protection и TLS 1.2+ включены;
- публичны только `/sub/<slug>`, `/sub/<slug>/open` и `/ca.crt`;
- subscription slug имеет минимум 128 бит случайной энтропии и является bearer secret;
- WB QR/URI OLCRTC Client содержит полный auth token и должен передаваться как credential; UI маскирует token до явного показа;
- subscription QR может содержать `mirror_key`; UI маскирует его до явного показа;
- log output редактирует tokens, proxy passwords, cookies и secret-bearing URI.

## Private CA

Installer создаёт локальный CA и leaf certificate с SAN для public IP, `127.0.0.1` и `localhost`. CA сохраняется при регенерации leaf. HSTS по умолчанию выключен.

Скачанный через ещё не доверенное соединение `ca.crt` нельзя считать автоматически подлинным. Сверьте SHA-256 fingerprint с выводом installer в консоли VPS, затем установите CA в trust store нужного клиента.

## Backup

Обычный UI backup не включает master key, `key.hex`, private TLS keys, tokens и WB profile. SQLite содержит только зашифрованные секреты, а YAML добавляется в redacted виде.

Recovery backup при uninstall содержит данные для полного восстановления, включая секреты. Он создаётся с mode `0600`; храните и передавайте его как credential material.

## Сообщение об уязвимости

Не публикуйте рабочие tokens, URI, subscription slugs или recovery archives в issue. Передавайте минимальный воспроизводимый пример владельцу репозитория через приватный security advisory GitHub.
