CREATE TABLE "parent" ("a" INTEGER, "b" INTEGER, PRIMARY KEY ("a", "b"));
CREATE TABLE "child" ("x" INTEGER, "y" INTEGER, CONSTRAINT "fk_first" FOREIGN KEY ("x", "y") REFERENCES "parent" ("a", "b"), CONSTRAINT "fk_second" FOREIGN KEY ("y", "x") REFERENCES "parent" ("a", "b"));
