/*
 * Minimal git-plumbing command for Meads.
 *
 * This links the libgit2 source prepared by wasm-git and intentionally exposes
 * only Meads' local object/ref hot path. The repository is mounted at /git by
 * wazero through WASI Preview 1. Network and worktree commands stay native.
 */
#include <git2.h>
#include <git2/transaction.h>

#include <errno.h>
#include <pwd.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>

#define ZERO_OID "0000000000000000000000000000000000000000"

/* libgit2 initializes its socket stream unconditionally, even when every
 * network backend is disabled. WASI Preview 1 has no sockets. */
int git_socket_stream__connect_timeout = 0;
int git_socket_stream__timeout = 0;
int git_socket_stream_global_init(void) { return 0; }
int git_socket_stream_new(void **out, const char *host, const char *port)
{
	(void)out;
	(void)host;
	(void)port;
	return -1;
}

/* WASI has no user, group, or process hierarchy. libgit2 uses these values for
 * ownership checks and PRNG salt only, so one stable sandbox identity is
 * sufficient. */
uid_t getuid(void) { return 0; }
uid_t geteuid(void) { return 0; }
gid_t getgid(void) { return 0; }
pid_t getppid(void) { return 1; }
pid_t getpgid(pid_t pid) { (void)pid; return 1; }
pid_t getsid(pid_t pid) { (void)pid; return 1; }
int getpwuid_r(uid_t uid, struct passwd *pwd, char *buf, size_t buflen,
	struct passwd **result)
{
	(void)uid;
	(void)pwd;
	(void)buf;
	(void)buflen;
	*result = NULL;
	return 0;
}

struct ref_update {
	char *name;
	git_oid next;
	git_oid prev;
	int remove;
	int has_prev;
};

static int git_failure(const char *operation)
{
	const git_error *error = git_error_last();
	fprintf(stderr, "%s: %s\n", operation,
		error && error->message ? error->message : "libgit2 error");
	return 128;
}

static int usage(const char *message)
{
	fprintf(stderr, "meads-git: %s\n", message);
	return 129;
}

static void print_oid(const git_oid *oid)
{
	char text[GIT_OID_SHA1_HEXSIZE + 1];
	git_oid_tostr(text, sizeof(text), oid);
	fputs(text, stdout);
}

static int oid_is_zero(const git_oid *oid)
{
	git_oid zero;
	memset(&zero, 0, sizeof(zero));
	return git_oid_equal(oid, &zero);
}

static int read_stdin_all(char **out, size_t *out_len)
{
	char *data = NULL;
	size_t len = 0, cap = 0;

	for (;;) {
		size_t got;
		if (cap - len < 4096) {
			size_t next = cap ? cap * 2 : 4096;
			char *grown = (char *)realloc(data, next);
			if (!grown) {
				free(data);
				return -1;
			}
			data = grown;
			cap = next;
		}
		got = fread(data + len, 1, cap - len, stdin);
		len += got;
		if (got == 0) {
			if (ferror(stdin)) {
				free(data);
				return -1;
			}
			break;
		}
	}
	*out = data;
	*out_len = len;
	return 0;
}

static int command_hash_object(git_repository *repo, int argc, char **argv)
{
	git_odb *odb = NULL;
	git_oid oid;
	char *data = NULL;
	size_t len = 0;
	int error;
	(void)argc;
	(void)argv;

	if (read_stdin_all(&data, &len) < 0)
		return usage("could not read stdin");
	if ((error = git_repository_odb(&odb, repo)) < 0 ||
		(error = git_odb_write(&oid, odb, data, len, GIT_OBJECT_BLOB)) < 0) {
		free(data);
		git_odb_free(odb);
		return git_failure("hash-object");
	}
	print_oid(&oid);
	fputc('\n', stdout);
	free(data);
	git_odb_free(odb);
	return 0;
}

static int command_mktree(git_repository *repo, int argc, char **argv)
{
	git_treebuilder *builder = NULL;
	git_oid tree_oid;
	char *input = NULL, *line, *next;
	size_t len = 0;
	int error = 0;
	(void)argc;
	(void)argv;

	if (read_stdin_all(&input, &len) < 0)
		return usage("could not read mktree input");
	if (!input) {
		input = (char *)calloc(1, 1);
		if (!input)
			return usage("out of memory");
	} else {
		char *terminated = (char *)realloc(input, len + 1);
		if (!terminated) {
			free(input);
			return usage("out of memory");
		}
		input = terminated;
		input[len] = '\0';
	}
	if (git_treebuilder_new(&builder, repo, NULL) < 0) {
		free(input);
		return git_failure("mktree");
	}

	for (line = input; line && *line; line = next) {
		char mode_text[16], type_text[16], oid_text[64];
		char *tab, *name;
		git_oid oid;
		git_filemode_t mode;
		next = strchr(line, '\n');
		if (next)
			*next++ = '\0';
		tab = strchr(line, '\t');
		if (!tab || sscanf(line, "%15s %15s %63s", mode_text, type_text, oid_text) != 3) {
			error = usage("invalid mktree input");
			break;
		}
		name = tab + 1;
		mode = (git_filemode_t)strtoul(mode_text, NULL, 8);
		if (git_oid_fromstr(&oid, oid_text) < 0 ||
			git_treebuilder_insert(NULL, builder, name, &oid, mode) < 0) {
			error = git_failure("mktree");
			break;
		}
		(void)type_text;
	}

	if (!error && git_treebuilder_write(&tree_oid, builder) < 0)
		error = git_failure("mktree");
	if (!error) {
		print_oid(&tree_oid);
		fputc('\n', stdout);
	}
	git_treebuilder_free(builder);
	free(input);
	return error;
}

static int command_commit_tree(git_repository *repo, int argc, char **argv)
{
	git_oid tree_oid, commit_oid;
	git_tree *tree = NULL;
	git_commit **parents = NULL;
	git_signature *signature = NULL;
	const char *message = "";
	size_t parent_count = 0, i;
	int error = 0;

	if (argc < 1 || git_oid_fromstr(&tree_oid, argv[0]) < 0)
		return usage("commit-tree requires a tree oid");
	for (i = 1; i < (size_t)argc; i++) {
		if (!strcmp(argv[i], "-p") && i + 1 < (size_t)argc) {
			parent_count++;
			i++;
		} else if (!strcmp(argv[i], "-m") && i + 1 < (size_t)argc) {
			message = argv[++i];
		} else {
			return usage("unsupported commit-tree option");
		}
	}
	if (git_tree_lookup(&tree, repo, &tree_oid) < 0)
		return git_failure("commit-tree");
	if (parent_count) {
		parents = (git_commit **)calloc(parent_count, sizeof(*parents));
		if (!parents) {
			git_tree_free(tree);
			return usage("out of memory");
		}
	}
	parent_count = 0;
	for (i = 1; i < (size_t)argc; i++) {
		if (!strcmp(argv[i], "-p")) {
			git_oid parent_oid;
			if (git_oid_fromstr(&parent_oid, argv[++i]) < 0 ||
				git_commit_lookup(&parents[parent_count], repo, &parent_oid) < 0) {
				error = git_failure("commit-tree parent");
				goto done;
			}
			parent_count++;
		} else if (!strcmp(argv[i], "-m")) {
			i++;
		}
	}
	if (git_signature_now(&signature, "meads", "meads@localhost") < 0 ||
		git_commit_create(&commit_oid, repo, NULL, signature, signature, NULL,
			message, tree, parent_count, (const git_commit **)parents) < 0) {
		error = git_failure("commit-tree");
		goto done;
	}
	print_oid(&commit_oid);
	fputc('\n', stdout);

done:
	for (i = 0; i < parent_count; i++)
		git_commit_free(parents[i]);
	free(parents);
	git_signature_free(signature);
	git_tree_free(tree);
	return error;
}

static int ref_matches(const char *name, int pattern_count, char **patterns)
{
	int i;
	if (!pattern_count)
		return 1;
	for (i = 0; i < pattern_count; i++) {
		size_t len = strlen(patterns[i]);
		if (!strncmp(name, patterns[i], len))
			return 1;
	}
	return 0;
}

static int command_for_each_ref(git_repository *repo, int argc, char **argv)
{
	git_reference_iterator *iterator = NULL;
	git_reference *ref = NULL;
	int with_oid = 0, first_pattern = 0, error;
	int i;

	for (i = 0; i < argc; i++) {
		if (!strncmp(argv[i], "--format=", 9)) {
			with_oid = strstr(argv[i] + 9, "%(objectname)") != NULL;
			first_pattern = i + 1;
		}
	}
	if (git_reference_iterator_new(&iterator, repo) < 0)
		return git_failure("for-each-ref");
	while ((error = git_reference_next(&ref, iterator)) == 0) {
		const char *name = git_reference_name(ref);
		git_reference *resolved = NULL;
		const git_oid *target;
		if (!ref_matches(name, argc - first_pattern, argv + first_pattern)) {
			git_reference_free(ref);
			ref = NULL;
			continue;
		}
		if (git_reference_type(ref) == GIT_REFERENCE_SYMBOLIC) {
			if (git_reference_resolve(&resolved, ref) < 0) {
				git_reference_free(ref);
				ref = NULL;
				continue;
			}
		}
		target = git_reference_target(resolved ? resolved : ref);
		fputs(name, stdout);
		if (with_oid && target) {
			fputc(' ', stdout);
			print_oid(target);
		}
		fputc('\n', stdout);
		git_reference_free(resolved);
		git_reference_free(ref);
		ref = NULL;
	}
	git_reference_iterator_free(iterator);
	if (error != GIT_ITEROVER)
		return git_failure("for-each-ref");
	return 0;
}

static int write_object(git_object *object, int batch, const char *spec)
{
	git_object_t type = git_object_type(object);
	const git_oid *oid = git_object_id(object);
	const void *data;
	size_t size;
	if (type != GIT_OBJECT_BLOB)
		return usage("cat-file currently supports blobs only");
	data = git_blob_rawcontent((git_blob *)object);
	size = (size_t)git_blob_rawsize((git_blob *)object);
	if (batch) {
		print_oid(oid);
		fprintf(stdout, " blob %lu\n", (unsigned long)size);
	}
	if (size && fwrite(data, 1, size, stdout) != size)
		return usage("could not write cat-file output");
	if (batch)
		fputc('\n', stdout);
	(void)spec;
	return 0;
}

static int command_cat_file_batch(git_repository *repo)
{
	char *input = NULL, *line, *next;
	size_t len = 0;
	int error = 0;
	if (read_stdin_all(&input, &len) < 0)
		return usage("could not read cat-file batch input");
	if (!input)
		return 0;
	input = (char *)realloc(input, len + 1);
	if (!input)
		return usage("out of memory");
	input[len] = '\0';
	for (line = input; line && *line; line = next) {
		git_object *object = NULL;
		next = strchr(line, '\n');
		if (next)
			*next++ = '\0';
		if (!*line)
			continue;
		if (git_revparse_single(&object, repo, line) < 0) {
			fprintf(stdout, "%s missing\n", line);
			continue;
		}
		error = write_object(object, 1, line);
		git_object_free(object);
		if (error)
			break;
	}
	free(input);
	return error;
}

static int command_cat_file(git_repository *repo, int argc, char **argv)
{
	git_object *object = NULL;
	int error;
	if (argc == 1 && !strcmp(argv[0], "--batch"))
		return command_cat_file_batch(repo);
	if (argc != 2 || strcmp(argv[0], "blob"))
		return usage("cat-file requires 'blob <object>' or '--batch'");
	if (git_revparse_single(&object, repo, argv[1]) < 0)
		return git_failure("cat-file");
	error = write_object(object, 0, argv[1]);
	git_object_free(object);
	return error;
}

static void free_updates(struct ref_update *updates, size_t count)
{
	size_t i;
	for (i = 0; i < count; i++)
		free(updates[i].name);
	free(updates);
}

static int apply_updates(git_repository *repo, struct ref_update *updates, size_t count)
{
	git_transaction *transaction = NULL;
	git_signature *signature = NULL;
	size_t i;
	int error = 0;

	if (git_transaction_new(&transaction, repo) < 0)
		return git_failure("update-ref transaction");
	for (i = 0; i < count; i++) {
		if (git_transaction_lock_ref(transaction, updates[i].name) < 0) {
			error = git_failure("update-ref lock");
			goto done;
		}
	}
	for (i = 0; i < count; i++) {
		git_oid current;
		int found = git_reference_name_to_id(&current, repo, updates[i].name);
		if (found == GIT_ENOTFOUND) {
			memset(&current, 0, sizeof(current));
		} else if (found < 0) {
			error = git_failure("update-ref read");
			goto done;
		}
		if (updates[i].has_prev && !git_oid_equal(&current, &updates[i].prev)) {
			fprintf(stderr, "update-ref: %s changed\n", updates[i].name);
			error = 1;
			goto done;
		}
	}
	if (git_signature_now(&signature, "meads", "meads@localhost") < 0) {
		error = git_failure("update-ref signature");
		goto done;
	}
	for (i = 0; i < count; i++) {
		int result = updates[i].remove
			? git_transaction_remove(transaction, updates[i].name)
			: git_transaction_set_target(transaction, updates[i].name,
				&updates[i].next, signature, "meads update");
		if (result < 0) {
			error = git_failure("update-ref prepare");
			goto done;
		}
	}
	if (git_transaction_commit(transaction) < 0)
		error = git_failure("update-ref commit");

done:
	git_signature_free(signature);
	git_transaction_free(transaction);
	return error;
}

static int parse_update_line(struct ref_update *update, char *line)
{
	char *kind, *name, *next, *prev;
	kind = strtok(line, " ");
	name = strtok(NULL, " ");
	if (!kind || !name)
		return -1;
	memset(update, 0, sizeof(*update));
	update->name = strdup(name);
	if (!update->name)
		return -1;
	if (!strcmp(kind, "delete")) {
		prev = strtok(NULL, " ");
		update->remove = 1;
		if (prev) {
			update->has_prev = 1;
			if (git_oid_fromstr(&update->prev, prev) < 0)
				return -1;
		}
		return 0;
	}
	if (strcmp(kind, "update"))
		return -1;
	next = strtok(NULL, " ");
	prev = strtok(NULL, " ");
	if (!next || git_oid_fromstr(&update->next, next) < 0)
		return -1;
	if (prev) {
		update->has_prev = 1;
		if (git_oid_fromstr(&update->prev, prev) < 0)
			return -1;
	}
	return 0;
}

static int command_update_ref_stdin(git_repository *repo)
{
	struct ref_update *updates = NULL;
	size_t count = 0, cap = 0, len = 0;
	char *input = NULL, *line, *next;
	int error = 0;

	if (read_stdin_all(&input, &len) < 0)
		return usage("could not read update-ref input");
	if (!input)
		return 0;
	input = (char *)realloc(input, len + 1);
	if (!input)
		return usage("out of memory");
	input[len] = '\0';
	for (line = input; line && *line; line = next) {
		next = strchr(line, '\n');
		if (next)
			*next++ = '\0';
		if (!*line || !strcmp(line, "start") || !strcmp(line, "prepare") ||
			!strcmp(line, "commit"))
			continue;
		if (count == cap) {
			size_t new_cap = cap ? cap * 2 : 8;
			struct ref_update *grown = (struct ref_update *)realloc(
				updates, new_cap * sizeof(*updates));
			if (!grown) {
				error = usage("out of memory");
				goto done;
			}
			updates = grown;
			cap = new_cap;
		}
		if (parse_update_line(&updates[count], line) < 0) {
			error = usage("invalid update-ref input");
			goto done;
		}
		count++;
	}
	error = apply_updates(repo, updates, count);

done:
	free(input);
	free_updates(updates, count);
	return error;
}

static int command_update_ref(git_repository *repo, int argc, char **argv)
{
	struct ref_update update;
	memset(&update, 0, sizeof(update));
	if (argc == 1 && !strcmp(argv[0], "--stdin"))
		return command_update_ref_stdin(repo);
	if (argc < 2 || argc > 3)
		return usage("update-ref requires <ref> <new> [<old>] or --stdin");
	update.name = argv[0];
	if (git_oid_fromstr(&update.next, argv[1]) < 0)
		return usage("invalid update-ref oid");
	update.remove = oid_is_zero(&update.next);
	if (argc == 3) {
		update.has_prev = 1;
		if (git_oid_fromstr(&update.prev, argv[2]) < 0)
			return usage("invalid update-ref old oid");
	}
	return apply_updates(repo, &update, 1);
}

static int command_rev_list(git_repository *repo, int argc, char **argv)
{
	git_object *start = NULL;
	git_revwalk *walk = NULL;
	git_oid oid;
	int result;
	if (argc != 1)
		return usage("rev-list requires one revision");
	if (git_revparse_single(&start, repo, argv[0]) < 0 ||
		git_revwalk_new(&walk, repo) < 0 ||
		git_revwalk_push(walk, git_object_id(start)) < 0) {
		git_object_free(start);
		git_revwalk_free(walk);
		return git_failure("rev-list");
	}
	while ((result = git_revwalk_next(&oid, walk)) == 0) {
		print_oid(&oid);
		fputc('\n', stdout);
	}
	git_revwalk_free(walk);
	git_object_free(start);
	if (result != GIT_ITEROVER)
		return git_failure("rev-list");
	return 0;
}

static int dispatch(git_repository *repo, const char *command, int argc, char **argv)
{
	if (!strcmp(command, "hash-object"))
		return command_hash_object(repo, argc, argv);
	if (!strcmp(command, "mktree"))
		return command_mktree(repo, argc, argv);
	if (!strcmp(command, "commit-tree"))
		return command_commit_tree(repo, argc, argv);
	if (!strcmp(command, "for-each-ref"))
		return command_for_each_ref(repo, argc, argv);
	if (!strcmp(command, "cat-file"))
		return command_cat_file(repo, argc, argv);
	if (!strcmp(command, "update-ref"))
		return command_update_ref(repo, argc, argv);
	if (!strcmp(command, "rev-list"))
		return command_rev_list(repo, argc, argv);
	return usage("unsupported command");
}

int main(int argc, char **argv)
{
	git_repository *repo = NULL;
	const char *git_dir = getenv("MEADS_GIT_DIR");
	int command_pos = 1, result;

	while (command_pos + 1 < argc && !strcmp(argv[command_pos], "-c"))
		command_pos += 2;
	if (command_pos >= argc)
		return usage("missing command");
	if (git_libgit2_init() < 0)
		return git_failure("libgit2 init");
	if (!git_dir)
		git_dir = "/git";
	if (git_repository_open_bare(&repo, git_dir) < 0) {
		result = git_failure("open repository");
		goto done;
	}
	result = dispatch(repo, argv[command_pos], argc - command_pos - 1,
		argv + command_pos + 1);

done:
	fflush(stdout);
	fflush(stderr);
	git_repository_free(repo);
	git_libgit2_shutdown();
	return result;
}
