"""Tests for the VM deploy's settings checks and templates, rendered by Ansible
itself on this machine (deploy/ansible/tests/render.yml), without a VM.

They need ansible-playbook, as the VM deploy does (deploy/README.md). Run
with: python3 -I -m unittest discover -s deploy -p 'test_*.py'
"""

import copy
import json
import pathlib
import shutil
import subprocess
import tempfile
import unittest

ANSIBLE = pathlib.Path(__file__).resolve().parent / "ansible"
SAMPLE = ANSIBLE / "tests" / "sample-vm.yml"
BACKUPS = {
    "bucket": "mpl-backups-123456789012",
    "region": "eu-central-1",
    "prefix": "postgres/",
    "metric_namespace": "Lobby/VM",
    "metric_name": "DatabaseBackups",
    "access_key_id": "AKIAEXAMPLE",
    "secret_access_key": "not-a-secret",
}


def settings(text):
    """KEY=value lines, as scripts/aws.sh and Terraform read lobby.env."""
    return dict(line.split("=", 1) for line in text.splitlines() if line and not line.startswith("#"))


def render(directory, *extra_variables):
    """Render the templates with the sample settings and extra -e values.

    Returns the finished process; the files are in directory/out.
    """
    if shutil.which("ansible-playbook") is None:
        raise AssertionError("Install Ansible first, e.g. 'uv tool install ansible-core' (deploy/README.md).")
    local = directory / "local"
    (local / "secrets").mkdir(parents=True, exist_ok=True)
    (local / "secrets" / "database_backups.json").write_text(json.dumps(BACKUPS))
    arguments = ["ansible-playbook", "-i", "localhost,", "-c", "local", "tests/render.yml", "-e", f"@{SAMPLE}",
                 "-e", json.dumps({"deploy_local_dir": str(local), "render_dir": str(directory / "out")})]
    for variables in extra_variables:
        arguments += ["-e", json.dumps(variables)]
    return subprocess.run(arguments, cwd=ANSIBLE, capture_output=True, text=True)


def sample_tenants():
    """The sample's tenants, read through Ansible's own YAML: the test has no YAML parser."""
    result = subprocess.run(
        ["ansible", "localhost", "-i", "localhost,", "-c", "local", "-m", "ansible.builtin.debug",
         "-a", "var=tenants", "-e", f"@{SAMPLE}", "-o"],
        cwd=ANSIBLE, capture_output=True, text=True, check=True)
    return json.loads(result.stdout.split("=>", 1)[1])["tenants"]


class RenderedTemplatesTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.directory = pathlib.Path(tempfile.mkdtemp(prefix="mpl-vm-templates-"))
        result = render(cls.directory)
        if result.returncode != 0:
            raise AssertionError(f"render.yml failed:\n{result.stdout}\n{result.stderr}")
        cls.out = cls.directory / "out"
        cls.secrets = cls.directory / "local" / "secrets"

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.directory)

    def read(self, name):
        return (self.out / name).read_text()

    def secret(self, name):
        return (self.secrets / name).read_text().strip()

    def test_the_lobby_gets_its_database_centrifugo_and_every_tenant(self):
        values = settings(self.read("vm/lobby.env"))
        self.assertEqual(values["LOBBY_DATABASE_URL"],
                         f"postgres://lobby:{self.secret('postgres_lobby_password')}@vm.example.org:5432/lobby"
                         "?sslmode=require")
        self.assertEqual(values["LOBBY_PUBLIC_URL"], "https://lobby.example.org")
        self.assertEqual(values["LOBBY_CENTRIFUGO_PUBLIC_URL"], "wss://vm.example.org/connection/websocket")
        self.assertEqual(values["LOBBY_CENTRIFUGO_API_URL"], "https://vm.example.org/api")
        self.assertEqual(values["LOBBY_CENTRIFUGO_API_KEY"], self.secret("centrifugo_api_key"))
        self.assertEqual(values["LOBBY_CONNECTION_TOKEN_HMAC_SECRET"], self.secret("connection_token_hmac_secret"))
        self.assertEqual(values["LOBBY_PROXY_SECRET"], self.secret("lobby_proxy_secret"))
        key = self.secret("lobby_player_token_key")
        self.assertRegex(values["LOBBY_PLAYER_TOKEN_KEYS"], f"^[0-9a-f]{{12}}:{key}$")
        self.assertEqual(values["LOBBY_ALLOWED_ORIGINS"], "https://game.example.org,https://second.example.org")
        games = json.loads(values["LOBBY_GAMES"])
        self.assertEqual([game["key"] for game in games], ["example-game", "second-game"])
        self.assertEqual(games, [tenant["lobby_game"] for tenant in sample_tenants()])
        self.assertEqual(values["LOBBY_GAME_SECRET_EXAMPLE_GAME"], self.secret("tenants/example-game/game_secret"))
        self.assertEqual(values["LOBBY_GAME_SECRET_SECOND_GAME"], self.secret("tenants/second-game/game_secret"))
        # LOBBY_GAMES is JSON on one line, with nothing that Terraform's
        # KEY=value reading would split.
        self.assertNotIn("\n", values["LOBBY_GAMES"])
        self.assertNotIn(" ", values["LOBBY_GAMES"].replace("Example Game", "").replace("Second Game", ""))

    def test_each_tenant_gets_its_database_its_secret_and_centrifugos_key(self):
        values = settings(self.read("tenants/example-game.env"))
        password = self.secret("tenants/example-game/postgres_password")
        self.assertEqual(values["DATABASE_URL"],
                         f"postgres://example_game:{password}@vm.example.org:5432/example_game?sslmode=require")
        self.assertEqual((values["DATABASE_NAME"], values["DATABASE_ROLE"], values["DATABASE_PASSWORD"]),
                         ("example_game", "example_game", password))
        self.assertEqual(values["LOBBY_URL"], "https://lobby.example.org")
        self.assertEqual(values["LOBBY_GAME_KEY"], "example-game")
        self.assertEqual(values["LOBBY_GAME_SECRET"], self.secret("tenants/example-game/game_secret"))
        self.assertEqual(values["CENTRIFUGO_API_KEY"], self.secret("centrifugo_api_key"))
        self.assertEqual(values["CENTRIFUGO_API_URL"], "https://vm.example.org/api")
        # A tenant's file holds its own secrets, not another tenant's or the lobby's.
        second = settings(self.read("tenants/second-game.env"))
        self.assertNotEqual(second["DATABASE_PASSWORD"], password)
        self.assertNotIn(self.secret("postgres_lobby_password"), self.read("tenants/example-game.env"))
        self.assertNotIn(self.secret("lobby_player_token_key"), self.read("tenants/example-game.env"))

    def test_postgres_admits_each_role_to_its_own_database_over_tls_only(self):
        rules = {tuple(line.split()) for line in self.read("vm/pg_hba.conf").splitlines()
                 if line and not line.startswith("#")}
        expected = {("local", "all", "all", "trust")}
        for database in ("lobby", "example_game", "second_game"):
            for role in (database, "example_game_admin"):
                for address in ("0.0.0.0/0", "::/0"):
                    expected.add(("hostssl", database, role, address, "scram-sha-256"))
        self.assertEqual(rules, expected)

    def test_centrifugo_accepts_the_lobby_page_and_the_tenants_and_proxies_to_the_configured_target(self):
        config = self.read("vm/centrifugo-config.toml")
        self.assertIn('allowed_origins = ["https://lobby.example.org", "https://game.example.org", '
                      '"https://second.example.org", "tauri://localhost", "http://tauri.localhost"]', config)
        # Until the cutover, the proxy calls go where they went before: the
        # address that centrifugo_proxy_url names, without its slash.
        self.assertIn('endpoint = "https://game-backend.lambda-url.eu-central-1.on.aws/internal/lobby/centrifugo/'
                      'subscribe"', config)
        self.assertIn('endpoint = "https://game-backend.lambda-url.eu-central-1.on.aws/internal/lobby/centrifugo/'
                      'sub-refresh"', config)
        self.assertIn('host_whitelist = ["vm.example.org"]', config)

    def test_centrifugos_channels_match_the_development_configuration(self):
        development = (ANSIBLE.parent.parent / "centrifugo" / "centrifugo-config.toml").read_text()

        def namespaces(text):
            """The channel settings, without comments."""
            section = text[text.index("[channel.without_namespace]"):]
            section = section.split("\n[admin]")[0]
            return [line for line in section.splitlines() if line and not line.startswith("#")]

        self.assertEqual(namespaces(self.read("vm/centrifugo-config.toml")), namespaces(development))

    def test_a_vm_taken_over_keeps_its_compose_project_and_paths(self):
        compose = self.read("vm/compose.yaml")
        self.assertIn("\nname: example-game\n", compose)
        self.assertIn("hba_file=/etc/example-game/postgres/pg_hba.conf", compose)
        self.assertIn("./postgres:/etc/example-game/postgres:ro", compose)
        self.assertIn('example-game.config-checksum: "postgres-checksum"', compose)
        self.assertIn("postgres_data:/var/lib/postgresql", compose)
        computed = json.loads(self.read("computed.json"))
        self.assertEqual(computed["vm_app_dir"], "/opt/example-game")

    def test_the_backup_dumps_every_database_under_its_own_names(self):
        values = settings(self.read("vm/backup.env"))
        self.assertEqual(values["MPL_BACKUP_DATABASES"], "lobby example_game second_game")
        self.assertEqual(values["MPL_BACKUP_APP_DIR"], "/opt/example-game")
        self.assertEqual(values["MPL_BACKUP_BUCKET"], "mpl-backups-123456789012")
        self.assertEqual(json.loads(self.read("computed.json"))["app_backup_settings_dir"], "/etc/mpl-backup")
        self.assertIn("EnvironmentFile=/etc/mpl-backup/backup.env", self.read("vm/mpl-backup.service"))
        self.assertIn("OnCalendar=*-*-* 02:00:00 UTC", self.read("vm/mpl-backup.timer"))

    def test_the_admin_role_reads_every_database_but_never_bearer_hashes(self):
        role = self.read("vm/admin-role.sql")
        for database in ("lobby", "example_game", "second_game"):
            self.assertIn(f'GRANT CONNECT ON DATABASE "{database}" TO "example_game_admin";', role)
            grants = self.read(f"admin-grants/{database}.sql")
            self.assertIn("GRANT SELECT (id, player_id, created_at, expires_at) ON public.lobby_sessions", grants)
        lobby = self.read("admin-grants/lobby.sql")
        # In the lobby's database, default privileges only once lobby_sessions
        # exists, inside the block that hides bearer_hash.
        self.assertEqual(lobby.count("ALTER DEFAULT PRIVILEGES"), 1)
        self.assertLess(lobby.index("IF to_regclass('public.lobby_sessions') IS NOT NULL"),
                        lobby.index('ALTER DEFAULT PRIVILEGES FOR ROLE "lobby"'))
        tenant = self.read("admin-grants/example_game.sql")
        self.assertIn('ALTER DEFAULT PRIVILEGES FOR ROLE "example_game" IN SCHEMA public', tenant)

    def test_each_database_is_created_with_its_own_role(self):
        sql = self.read("create-database/second_game.sql")
        self.assertIn("CREATE DATABASE %I OWNER %I', 'second_game', 'second_game'", sql)
        self.assertIn("PASSWORD 'password-of-second_game'", sql)
        self.assertNotIn("LOGIN;", sql.split("PASSWORD")[1])

    def test_the_secrets_the_deploy_requires_include_each_tenants(self):
        files = json.loads(self.read("computed.json"))["app_secret_files"]
        names = [pathlib.Path(path).relative_to(self.secrets).as_posix() for path in files]
        self.assertEqual(names, [
            "postgres_superuser_password", "postgres_lobby_password", "postgres_admin_password",
            "centrifugo_api_key", "connection_token_hmac_secret", "lobby_proxy_secret", "lobby_player_token_key",
            "postgres-tls/server.key", "postgres-tls/server.crt",
            "tenants/example-game/postgres_password", "tenants/example-game/game_secret",
            "tenants/second-game/postgres_password", "tenants/second-game/game_secret",
        ])


class SettingsChecksTest(unittest.TestCase):
    """The deploy's settings checks stop before anything changes on the VM."""

    def setUp(self):
        self.directory = pathlib.Path(tempfile.mkdtemp(prefix="mpl-vm-settings-"))
        self.tenants = sample_tenants()

    def tearDown(self):
        shutil.rmtree(self.directory)

    def refused(self, variables, message):
        result = render(self.directory, variables)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn(message, result.stdout)

    def tenant(self, **changes):
        tenants = copy.deepcopy(self.tenants)
        tenants[0].update(changes)
        return {"tenants": tenants}

    def test_needs_a_tenant(self):
        self.refused({"tenants": []}, "at least one tenant")

    def test_needs_the_proxy_target(self):
        self.refused({"centrifugo_proxy_url": "http://insecure.example.org"}, "centrifugo_proxy_url")

    def test_refuses_a_tenant_that_takes_the_lobbys_database(self):
        self.refused(self.tenant(database="lobby"), "Tenant example-game")

    def test_refuses_a_lobby_game_with_another_secret_variable(self):
        lobby_game = dict(self.tenants[0]["lobby_game"], secret_env="LOBBY_GAME_SECRET_OTHER")
        self.refused(self.tenant(lobby_game=lobby_game), "secret_env LOBBY_GAME_SECRET_EXAMPLE_GAME")

    def test_refuses_a_lobby_game_with_another_key(self):
        lobby_game = dict(self.tenants[0]["lobby_game"], key="other-game")
        self.refused(self.tenant(lobby_game=lobby_game), "Tenant example-game")

    def test_refuses_a_plain_http_origin(self):
        self.refused(self.tenant(origins=["http://game.example.org"]), "Tenant example-game")

    def test_refuses_two_tenants_with_one_role(self):
        tenants = copy.deepcopy(self.tenants)
        tenants[1]["role"] = tenants[0]["role"]
        self.refused({"tenants": tenants}, "Each tenant needs its own key, database and role.")


if __name__ == "__main__":
    unittest.main()
