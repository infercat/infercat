FROM gcr.io/distroless/static-debian13:nonroot@sha256:1c2c046bc09ed40fad370b599a0b1ae7987f55b01e247cf27a7c27cd97e5bbc7 AS base
FROM base
# Named volumes inherit this directory's ownership on first use.
COPY --from=base --chown=65532:65532 /home/nonroot /data
COPY infercat /usr/local/bin/infercat
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/licenses/infercat/
LABEL org.opencontainers.image.source="https://github.com/infercat/infercat"
USER 65532:65532
WORKDIR /data
VOLUME ["/data"]
ENTRYPOINT ["infercat"]
CMD ["serve", "--data-dir", "/data"]
