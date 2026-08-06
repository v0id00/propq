## Summary

<!-- What does this PR do, in one or two sentences? -->

## Related issue

<!-- Link the issue(s) this closes, e.g. "Closes #12". "None" if N/A. -->

## Changes

<!-- Bullet list of what changed and why. Keep it factual. -->

- 

## Testing done

- [ ] `make test` passes
- [ ] `make lint` passes
- [ ] Manual run against a local/test server (paste the command + key output)

## Safety checklist

- [ ] Destructive SQL behavior unchanged or still gated behind `--force`
- [ ] No real credentials or hosts added — only `propq.toml.example` updated if config changed
- [ ] New behavior defaults to *off* (fail-closed) unless intentionally opt-in
- [ ] README updated if flags, config keys, or output formats changed
