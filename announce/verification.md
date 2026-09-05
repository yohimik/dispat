# Announcement verification

Verified on 5 September 2026 against Crier commit `7edaff9`.

The release calls Crier once. That command publishes one photo post to Instagram, LinkedIn, and Discord, plus an Instagram cover story with music. Discord sends its images, changelog caption, and explicit permission for `@everyone` in the same webhook request.

Validation completed:

- Crier's Docker release export passed its six target builds, binary smoke tests, full unit and integration suites, real FFmpeg tests, and existing coverage gates.
- Race tests passed for Crier's application, configuration, and publishing packages. Vet, formatting, and generated documentation checks passed.
- A real CLI invocation against local mock services completed all four publications. It verified ordered image attachments, exactly one Discord message, explicit mention permission, and a separate Instagram story video with no caption.
- Dispat's actual announcement script ran with the new Crier binary in dry-run mode. It made exactly one publishing call, shared the two rendered photos across all three destinations, preserved their changelog captions, and generated the additional story. A Discord-only cover replay also passed.
- FFprobe confirmed the generated story contains H.264 video at 1080 by 1920, AAC audio, and a duration of exactly sixteen seconds. The cover frame was visually reviewed.
- The announcement shell regression and Docker shell gate passed. Actionlint accepted the release and replay workflows.

No live social posts were sent during validation. The Discord credential is stored only as the repository secret `CRIER_PUBLISH_DISCORD_WEBHOOK_URL`.

A Crier release containing `7edaff9` is required before this Dispat release. The workflow runs `crier ping` as a release gate and transports that exact executable to the publishing job. Older Crier versions reject the new configuration at the gate. A successful credential check cannot guarantee that a later network request will succeed; failures remain visible per destination and are never blindly retried.
