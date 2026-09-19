ALTER TABLE sandbox_profiles DROP CONSTRAINT IF EXISTS sandbox_profiles_network_mode_check;
ALTER TABLE sandbox_profiles ADD CONSTRAINT sandbox_profiles_network_mode_check CHECK(network_mode IN ('none','qa-network','bridge-only'));
