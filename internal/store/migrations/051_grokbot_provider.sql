INSERT INTO provider_settings(provider, model, command, secret_env, base_url, options)
VALUES ('grokbot', 'grok-4', '', 'XAI_API_KEY', 'https://api.x.ai/v1', '{"models":["grok-4"],"efforts":["low","medium","high"]}')
ON CONFLICT (provider) DO NOTHING;
