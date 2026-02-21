"""Configuration via pydantic-settings + .env."""

from __future__ import annotations

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_file=".env",
        env_prefix="ERRATA_",
        env_file_encoding="utf-8",
        extra="ignore",
        populate_by_name=True,
    )

    # API keys (no prefix — standard env var names)
    anthropic_api_key: str | None = Field(default=None, alias="ANTHROPIC_API_KEY")
    openai_api_key: str | None = Field(default=None, alias="OPENAI_API_KEY")
    google_api_key: str | None = Field(default=None, alias="GOOGLE_API_KEY")

    # Active models — can be overridden via ERRATA_ACTIVE_MODELS=a,b,c
    active_models: list[str] = Field(default_factory=list)

    default_anthropic_model: str = "claude-sonnet-4-6"
    default_openai_model: str = "gpt-4o"
    default_gemini_model: str = "gemini-2.0-flash"

    preferences_path: str = "data/preferences.jsonl"

    @property
    def resolved_active_models(self) -> list[str]:
        """Return active models, falling back to one model per available provider."""
        if self.active_models:
            return self.active_models
        defaults = []
        if self.anthropic_api_key:
            defaults.append(self.default_anthropic_model)
        if self.openai_api_key:
            defaults.append(self.default_openai_model)
        if self.google_api_key:
            defaults.append(self.default_gemini_model)
        return defaults


settings = Settings()
