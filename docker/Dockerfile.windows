FROM plugins/base:windows-ltsc2022-amd64

USER ContainerAdministrator

ENV GODEBUG=netdns=go

ADD release/windows/amd64/drone-email.exe C:/drone-email.exe

ENTRYPOINT ["C:\\drone-email.exe"]
