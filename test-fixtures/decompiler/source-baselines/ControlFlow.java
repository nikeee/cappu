public class ControlFlow {

  static int sum(int arg0) {
    int var1 = 0;
    int var2 = 0;
    while (var2 < arg0) {
      var1 = var1 + var2;
      var2++;
    }
    return var1;
  }

  static int absish(int arg0) {
    if (arg0 < 0) {
      return -arg0;
    }
    return arg0;
  }

  static int countdown(int arg0) {
    int var1 = 0;
    while (arg0 > 0) {
      var1 = var1 + 1;
      arg0 = arg0 - 1;
    }
    return var1;
  }

  static int firstSet(int arg0) {
    int var1 = 0;
    do {
      var1 = var1 + 1;
    } while (var1 < arg0);
    return var1;
  }

  static boolean inRange(int arg0, int arg1, int arg2) {
    return arg0 >= arg1 && arg0 <= arg2;
  }

  public static void main(java.lang.String[] arg0) {
    java.lang.System.out.println(sum(5));
    java.lang.System.out.println(absish(-7));
    java.lang.System.out.println(countdown(3));
    java.lang.System.out.println(firstSet(4));
    java.lang.System.out.println(inRange(5, 1, 10));
  }
}
