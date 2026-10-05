import 'package:dio/dio.dart';
import 'package:fl_clash/common/request.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test(
    'managed distribution cannot offer unsigned upstream replacements',
    () async {
      final request = Request();
      var calls = 0;
      request.dio.interceptors.add(
        InterceptorsWrapper(
          onRequest: (options, handler) {
            calls++;
            handler.reject(DioException(requestOptions: options));
          },
        ),
      );
      expect(await request.checkForUpdate(), isNull);
      expect(calls, 0);
      request.dio.close(force: true);
    },
  );
}
